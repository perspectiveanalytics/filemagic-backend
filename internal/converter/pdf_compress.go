package converter

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type PDFCompressor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFCompressor(cfg *config.Config, runner *NsjailRunner) *PDFCompressor {
	return &PDFCompressor{cfg: cfg, runner: runner}
}

func (c *PDFCompressor) Type() queue.ConversionType {
	return queue.ConversionPDFCompress
}

func (c *PDFCompressor) Convert(ctx context.Context, job *queue.Job) error {
	// Check if target size mode is requested
	if ts, ok := job.Options["targetSize"].(float64); ok && ts > 0 {
		return c.compressToTargetSize(ctx, job, int64(ts))
	}

	return c.compressWithLevel(ctx, job)
}

// compressWithLevel is the original level-based compression path.
func (c *PDFCompressor) compressWithLevel(ctx context.Context, job *queue.Job) error {
	level, ok := job.Options["level"].(string)
	if !ok {
		level = "medium"
	}

	lossy := true
	if v, ok := job.Options["lossy"].(bool); ok {
		lossy = v
	}

	var pdfsettings string
	switch level {
	case "low":
		pdfsettings = "/prepress"
	case "medium":
		pdfsettings = "/ebook"
	case "high":
		pdfsettings = "/screen"
	default:
		pdfsettings = "/ebook"
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	cmd := c.buildGsCmd(inputFile, outputFile, pdfsettings, lossy, level)

	err = c.runner.Run(ctx, cmd, jobDir, "pdf.cfg", 60*time.Second)
	if err != nil {
		return err
	}

	outputInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	// If compressed PDF is larger than original, use the original
	if outputInfo.Size() >= job.InputSize && job.InputSize > 0 {
		slog.Info("compressed PDF is larger than original, using original",
			"inputSize", job.InputSize,
			"outputSize", outputInfo.Size(),
		)
		if err := copyFile(inputFile, outputFile); err != nil {
			return fmt.Errorf("failed to copy original: %w", err)
		}
		job.OutputPath = outputFile
		job.OutputSize = job.InputSize
		return nil
	}

	job.OutputPath = outputFile
	job.OutputSize = outputInfo.Size()

	return nil
}

func (c *PDFCompressor) buildGsCmd(inputFile, outputFile, pdfsettings string, lossy bool, level string) []string {
	cmd := []string{
		"/usr/bin/gs",
		"-dSAFER",
		"-dPARANOIDSAFER",
		"-dNOOUTERSAVE",
		"-dNEWPDF=true",
		"-dMaxBitmap=100000000",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		fmt.Sprintf("-dPDFSETTINGS=%s", pdfsettings),
		"-dNOPAUSE",
		"-dQUIET",
		"-dBATCH",
	}

	if lossy {
		var dpi int
		switch level {
		case "low":
			dpi = 300
		case "medium":
			dpi = 150
		case "high":
			dpi = 72
		default:
			dpi = 150
		}
		cmd = append(cmd,
			"-dDownsampleColorImages=true",
			"-dDownsampleGrayImages=true",
			"-dDownsampleMonoImages=true",
			fmt.Sprintf("-dColorImageResolution=%d", dpi),
			fmt.Sprintf("-dGrayImageResolution=%d", dpi),
			fmt.Sprintf("-dMonoImageResolution=%d", dpi),
		)
	}

	cmd = append(cmd,
		fmt.Sprintf("-sOutputFile=%s", outputFile),
		inputFile,
	)
	return cmd
}

// compressToTargetSize iterates through progressively aggressive compression
// settings to get the PDF under the target size.
func (c *PDFCompressor) compressToTargetSize(ctx context.Context, job *queue.Job, targetSize int64) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	// If input is already under target, just copy it
	if job.InputSize > 0 && job.InputSize <= targetSize {
		if err := copyFile(inputFile, outputFile); err != nil {
			return fmt.Errorf("failed to copy original: %w", err)
		}
		job.OutputPath = outputFile
		job.OutputSize = job.InputSize
		return nil
	}

	// Try increasingly aggressive settings until target is met.
	// Each step uses PDFSETTINGS + explicit DPI control.
	type attempt struct {
		pdfsettings string
		dpi         int
		label       string
	}

	// Initial DPI estimate based on target/input ratio
	ratio := float64(targetSize) / float64(job.InputSize)
	startDPI := int(300.0 * math.Sqrt(ratio))
	if startDPI > 300 {
		startDPI = 300
	}
	if startDPI < 36 {
		startDPI = 36
	}

	attempts := []attempt{
		{"/ebook", startDPI, fmt.Sprintf("ebook/%ddpi", startDPI)},
	}

	// Add more aggressive steps
	if startDPI > 100 {
		attempts = append(attempts, attempt{"/ebook", startDPI / 2, fmt.Sprintf("ebook/%ddpi", startDPI/2)})
	}
	attempts = append(attempts, attempt{"/screen", startDPI / 2, fmt.Sprintf("screen/%ddpi", startDPI/2)})
	if startDPI/3 >= 36 {
		attempts = append(attempts, attempt{"/screen", startDPI / 3, fmt.Sprintf("screen/%ddpi", startDPI/3)})
	}

	var bestSize int64
	for i, a := range attempts {
		dpi := a.dpi
		if dpi < 36 {
			dpi = 36
		}

		cmd := []string{
			"/usr/bin/gs",
			"-dSAFER",
			"-dPARANOIDSAFER",
			"-dNOOUTERSAVE",
			"-dNEWPDF=true",
			"-dMaxBitmap=100000000",
			"-sDEVICE=pdfwrite",
			"-dCompatibilityLevel=1.4",
			fmt.Sprintf("-dPDFSETTINGS=%s", a.pdfsettings),
			"-dNOPAUSE",
			"-dQUIET",
			"-dBATCH",
			"-dDownsampleColorImages=true",
			"-dDownsampleGrayImages=true",
			"-dDownsampleMonoImages=true",
			fmt.Sprintf("-dColorImageResolution=%d", dpi),
			fmt.Sprintf("-dGrayImageResolution=%d", dpi),
			fmt.Sprintf("-dMonoImageResolution=%d", dpi),
			fmt.Sprintf("-sOutputFile=%s", outputFile),
			inputFile,
		}

		if err := c.runner.Run(ctx, cmd, jobDir, "pdf.cfg", 60*time.Second); err != nil {
			return err
		}

		info, err := os.Stat(outputFile)
		if err != nil {
			return fmt.Errorf("output file not found: %w", err)
		}
		outSize := info.Size()

		slog.Info("pdf target size iteration",
			"iteration", i+1,
			"settings", a.label,
			"dpi", dpi,
			"outputSize", outSize,
			"targetSize", targetSize,
		)

		bestSize = outSize

		if outSize <= targetSize {
			break
		}
	}

	job.OutputPath = outputFile
	job.OutputSize = bestSize
	return nil
}
