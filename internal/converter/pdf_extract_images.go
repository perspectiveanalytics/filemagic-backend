package converter

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type PDFImageExtractor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFImageExtractor(cfg *config.Config, runner *NsjailRunner) *PDFImageExtractor {
	return &PDFImageExtractor{cfg: cfg, runner: runner}
}

func (c *PDFImageExtractor) Type() queue.ConversionType {
	return queue.ConversionPDFExtractImages
}

func (c *PDFImageExtractor) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	cmd := []string{
		"/usr/bin/pdfimages", "-all",
		job.InputPath,
		filepath.Join(outputDir, "img"),
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "poppler.cfg", 60*time.Second); err != nil {
		return err
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("failed to read output directory: %w", err)
	}

	var files []map[string]any
	var totalSize int64
	index := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if index >= 200 {
			break
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		totalSize += info.Size()
		mimeType := mime.TypeByExtension(filepath.Ext(entry.Name()))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		files = append(files, map[string]any{
			"name":  entry.Name(),
			"size":  info.Size(),
			"index": index,
			"type":  mimeType,
		})
		index++
	}

	if len(files) == 0 {
		return fmt.Errorf("no images found in PDF")
	}

	job.Metadata = map[string]any{
		"files":     files,
		"fileCount": len(files),
		"totalSize": totalSize,
	}
	job.OutputPath = outputDir
	job.OutputSize = totalSize
	return nil
}
