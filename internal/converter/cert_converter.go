package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type CertConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewCertConverter(cfg *config.Config, runner *NsjailRunner) *CertConverter {
	return &CertConverter{cfg: cfg, runner: runner}
}

func (c *CertConverter) Type() queue.ConversionType {
	return queue.ConversionCertConvert
}

func (c *CertConverter) Convert(ctx context.Context, job *queue.Job) error {
	targetFormat, ok := job.Options["targetFormat"].(string)
	if !ok || targetFormat == "" {
		return fmt.Errorf("missing target format")
	}

	password, _ := job.Options["password"].(string)
	outputPassword, _ := job.Options["outputPassword"].(string)

	// Detect source format from input file extension
	inputFile := job.InputPath
	sourceFormat := detectSourceFormat(inputFile)
	if sourceFormat == "" {
		return fmt.Errorf("unsupported source certificate format")
	}

	if sourceFormat == targetFormat {
		return fmt.Errorf("source and target formats are the same")
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	ext := formatExtension(targetFormat)
	outputFile := filepath.Join(outputDir, "output"+ext)

	// Direct conversions
	if canDirectConvert(sourceFormat, targetFormat) {
		cmd := buildConvertCommand(sourceFormat, targetFormat, inputFile, outputFile, password, outputPassword, outputDir)
		if cmd == nil {
			return fmt.Errorf("no conversion path from %s to %s", sourceFormat, targetFormat)
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "cert.cfg", 30*time.Second); err != nil {
			return err
		}
	} else {
		// Indirect: convert via PEM intermediate
		intermediatePEM := filepath.Join(outputDir, "intermediate.pem")

		// Step 1: source → PEM
		cmd1 := buildConvertCommand(sourceFormat, "pem", inputFile, intermediatePEM, password, "", outputDir)
		if cmd1 == nil {
			return fmt.Errorf("no conversion path from %s to PEM", sourceFormat)
		}
		if err := c.runner.Run(ctx, cmd1, jobDir, "cert.cfg", 30*time.Second); err != nil {
			return fmt.Errorf("step 1 (%s→PEM) failed: %w", sourceFormat, err)
		}

		// Step 2: PEM → target
		cmd2 := buildConvertCommand("pem", targetFormat, intermediatePEM, outputFile, "", outputPassword, outputDir)
		if cmd2 == nil {
			return fmt.Errorf("no conversion path from PEM to %s", targetFormat)
		}
		if err := c.runner.Run(ctx, cmd2, jobDir, "cert.cfg", 30*time.Second); err != nil {
			return fmt.Errorf("step 2 (PEM→%s) failed: %w", targetFormat, err)
		}
	}

	outputInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = outputInfo.Size()
	return nil
}

func detectSourceFormat(inputPath string) string {
	lower := strings.ToLower(inputPath)
	switch {
	case strings.HasSuffix(lower, ".pem"),
		strings.HasSuffix(lower, ".crt"),
		strings.HasSuffix(lower, ".cer"):
		return "pem"
	case strings.HasSuffix(lower, ".der"):
		return "der"
	case strings.HasSuffix(lower, ".p12"),
		strings.HasSuffix(lower, ".pfx"):
		return "p12"
	case strings.HasSuffix(lower, ".p7b"),
		strings.HasSuffix(lower, ".p7c"):
		return "p7b"
	default:
		return ""
	}
}

func formatExtension(format string) string {
	switch format {
	case "pem":
		return ".pem"
	case "der":
		return ".der"
	case "p12":
		return ".p12"
	case "p7b":
		return ".p7b"
	default:
		return ""
	}
}

func canDirectConvert(from, to string) bool {
	directPairs := map[string]bool{
		"pem:der": true, "der:pem": true,
		"pem:p12": true, "p12:pem": true,
		"pem:p7b": true, "p7b:pem": true,
	}
	return directPairs[from+":"+to]
}

// buildConvertCommand builds an OpenSSL command for certificate conversion.
// Passwords are passed via temporary files (file: prefix) to avoid exposure
// in /proc/<pid>/cmdline on the host. The passFiles are written into outputDir
// which is inside the sandbox working directory.
func buildConvertCommand(from, to, inputFile, outputFile, password, outputPassword, outputDir string) []string {
	switch from + ":" + to {
	case "pem:der":
		return []string{"/usr/bin/openssl", "x509", "-in", inputFile, "-outform", "DER", "-out", outputFile}
	case "der:pem":
		return []string{"/usr/bin/openssl", "x509", "-in", inputFile, "-inform", "DER", "-outform", "PEM", "-out", outputFile}
	case "pem:p12":
		pass := outputPassword
		if pass == "" {
			pass = "changeit"
		}
		passFile := writePassFile(outputDir, "passout", pass)
		if passFile == "" {
			return []string{"/usr/bin/openssl", "pkcs12", "-export", "-in", inputFile, "-out", outputFile, "-nokeys", "-passout", "pass:"}
		}
		return []string{"/usr/bin/openssl", "pkcs12", "-export", "-in", inputFile, "-out", outputFile, "-nokeys", "-passout", "file:" + passFile}
	case "p12:pem":
		passFile := writePassFile(outputDir, "passin", password)
		if passFile == "" {
			return []string{"/usr/bin/openssl", "pkcs12", "-in", inputFile, "-out", outputFile, "-nodes", "-passin", "pass:"}
		}
		return []string{"/usr/bin/openssl", "pkcs12", "-in", inputFile, "-out", outputFile, "-nodes", "-passin", "file:" + passFile}
	case "pem:p7b":
		return []string{"/usr/bin/openssl", "crl2pkcs7", "-nocrl", "-certfile", inputFile, "-out", outputFile}
	case "p7b:pem":
		return []string{"/usr/bin/openssl", "pkcs7", "-in", inputFile, "-print_certs", "-out", outputFile}
	default:
		return nil
	}
}

// writePassFile writes a password to a temporary file and returns its path.
// Returns empty string if the directory is empty or write fails.
// A trailing newline is always appended so OpenSSL's file: source can read
// even an empty password (a zero-byte file causes "Error reading password from BIO").
func writePassFile(dir, name, password string) string {
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, "."+name)
	if err := os.WriteFile(path, []byte(password+"\n"), 0600); err != nil {
		return ""
	}
	return path
}
