package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type PDFPasswordConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFPasswordConverter(cfg *config.Config, runner *NsjailRunner) *PDFPasswordConverter {
	return &PDFPasswordConverter{cfg: cfg, runner: runner}
}

func (c *PDFPasswordConverter) Type() queue.ConversionType {
	return queue.ConversionPDFPassword
}

func (c *PDFPasswordConverter) Convert(ctx context.Context, job *queue.Job) error {
	mode, _ := job.Options["mode"].(string)

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	var cmd []string

	switch mode {
	case "protect":
		userPass, _ := job.Options["userPassword"].(string)
		ownerPass, _ := job.Options["ownerPassword"].(string)
		if userPass == "" {
			return fmt.Errorf("user password is required for protection")
		}
		if ownerPass == "" {
			ownerPass = userPass
		}
		cmd = []string{
			"/usr/bin/qpdf",
			"--encrypt", userPass, ownerPass, "256",
			"--", inputFile, outputFile,
		}

	case "remove":
		password, _ := job.Options["password"].(string)
		cmd = []string{"/usr/bin/qpdf"}
		if password != "" {
			// Write password to file to avoid /proc/cmdline exposure
			passFile := filepath.Join(filepath.Dir(inputFile), ".password")
			if werr := os.WriteFile(passFile, []byte(password+"\n"), 0600); werr != nil {
				return fmt.Errorf("failed to write password file: %w", werr)
			}
			cmd = append(cmd, "--password-file="+passFile)
		}
		cmd = append(cmd, "--decrypt", inputFile, outputFile)

	default:
		return fmt.Errorf("invalid mode: %s (expected 'protect' or 'remove')", mode)
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "qpdf.cfg", 30*time.Second); err != nil {
		return err
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}
