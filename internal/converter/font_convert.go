package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/scripts"
)

type FontConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewFontConverter(cfg *config.Config, runner *NsjailRunner) *FontConverter {
	return &FontConverter{cfg: cfg, runner: runner}
}

func (c *FontConverter) Type() queue.ConversionType {
	return queue.ConversionFontConvert
}

func (c *FontConverter) Convert(ctx context.Context, job *queue.Job) error {
	targetFormat, _ := job.Options["targetFormat"].(string)
	if targetFormat == "" {
		return fmt.Errorf("missing target format")
	}

	validFormats := map[string]bool{"ttf": true, "otf": true, "woff": true, "woff2": true}
	if !validFormats[targetFormat] {
		return fmt.Errorf("invalid target format: %s", targetFormat)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output."+targetFormat)

	// Write the embedded font conversion script
	scriptPath := filepath.Join(jobDir, "font_convert.py")
	if err := os.WriteFile(scriptPath, scripts.FontConvertScript, 0644); err != nil {
		return fmt.Errorf("failed to write font conversion script: %w", err)
	}

	cmd := []string{
		"/usr/bin/python3",
		scriptPath,
		inputFile,
		outputFile,
		targetFormat,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "font.cfg", 30*time.Second); err != nil {
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
