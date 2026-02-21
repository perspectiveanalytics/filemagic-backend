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

type EbookConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewEbookConverter(cfg *config.Config, runner *NsjailRunner) *EbookConverter {
	return &EbookConverter{cfg: cfg, runner: runner}
}

func (c *EbookConverter) Type() queue.ConversionType {
	return queue.ConversionEbookConvert
}

func (c *EbookConverter) Convert(ctx context.Context, job *queue.Job) error {
	targetFormat, _ := job.Options["targetFormat"].(string)
	if targetFormat == "" {
		return fmt.Errorf("missing target format")
	}

	validFormats := map[string]bool{
		"epub": true, "mobi": true, "azw3": true,
		"txt": true, "fb2": true, "docx": true, "htmlz": true,
	}
	if !validFormats[targetFormat] {
		return fmt.Errorf("invalid target format: %s", targetFormat)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output."+targetFormat)

	cmd := []string{
		"/usr/bin/ebook-convert",
		inputFile,
		outputFile,
	}

	// 120 second timeout — ebook conversion can be slow for large files
	if err := c.runner.Run(ctx, cmd, jobDir, "ebook.cfg", 120*time.Second); err != nil {
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
