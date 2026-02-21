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

type MarkdownPDFConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewMarkdownPDFConverter(cfg *config.Config, runner *NsjailRunner) *MarkdownPDFConverter {
	return &MarkdownPDFConverter{cfg: cfg, runner: runner}
}

func (c *MarkdownPDFConverter) Type() queue.ConversionType {
	return queue.ConversionMarkdownPDF
}

func (c *MarkdownPDFConverter) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	cmd := []string{
		"/usr/bin/pandoc",
		inputFile,
		"-o", outputFile,
		"--pdf-engine=/usr/bin/typst",
		"--sandbox",
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "pandoc.cfg", 60*time.Second); err != nil {
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
