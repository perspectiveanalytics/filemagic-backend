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

type SvgToPngConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewSvgToPngConverter(cfg *config.Config, runner *NsjailRunner) *SvgToPngConverter {
	return &SvgToPngConverter{cfg: cfg, runner: runner}
}

func (c *SvgToPngConverter) Type() queue.ConversionType {
	return queue.ConversionSvgToPng
}

func (c *SvgToPngConverter) Convert(ctx context.Context, job *queue.Job) error {
	width, _ := job.Options["width"].(float64)
	height, _ := job.Options["height"].(float64)

	const maxSVGDim = 4096
	if width > maxSVGDim || height > maxSVGDim {
		return fmt.Errorf("requested dimensions %dx%d exceed maximum %d", int(width), int(height), maxSVGDim)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	outputFile := filepath.Join(outputDir, "output.png")

	// If no dimensions specified, enforce a default max to prevent
	// SVGs with huge viewBox from producing enormous PNGs (DoS).
	if width <= 0 && height <= 0 {
		width = 4096
	}

	cmd := []string{"/usr/bin/rsvg-convert"}
	if width > 0 {
		cmd = append(cmd, "-w", fmt.Sprintf("%d", int(width)))
	}
	if height > 0 {
		cmd = append(cmd, "-h", fmt.Sprintf("%d", int(height)))
	}
	cmd = append(cmd, job.InputPath, "-o", outputFile)

	if err := c.runner.Run(ctx, cmd, jobDir, "svg.cfg", 30*time.Second); err != nil {
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
