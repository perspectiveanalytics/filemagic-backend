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

type PDFRepairer struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFRepairer(cfg *config.Config, runner *NsjailRunner) *PDFRepairer {
	return &PDFRepairer{cfg: cfg, runner: runner}
}

func (c *PDFRepairer) Type() queue.ConversionType {
	return queue.ConversionPDFRepair
}

func (c *PDFRepairer) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	// qpdf rewrites the PDF, fixing structural issues like:
	// - Cross-reference table errors
	// - Invalid object references
	// - Stream length mismatches
	// - Missing required entries
	// --warning-exit-0 ensures non-fatal warnings don't cause failure
	cmd := []string{
		"/usr/bin/qpdf",
		"--warning-exit-0",
		inputFile,
		outputFile,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "qpdf.cfg", 30*time.Second); err != nil {
		return fmt.Errorf("PDF repair failed: %w", err)
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}
