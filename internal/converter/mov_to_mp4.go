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

type MovToMp4Converter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewMovToMp4Converter(cfg *config.Config, runner *NsjailRunner) *MovToMp4Converter {
	return &MovToMp4Converter{cfg: cfg, runner: runner}
}

func (c *MovToMp4Converter) Type() queue.ConversionType {
	return queue.ConversionMovToMp4
}

func (c *MovToMp4Converter) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	outputFile := filepath.Join(outputDir, "output.mp4")

	cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", job.InputPath,
		"-c:v", "libx264", "-c:a", "aac",
		"-movflags", "+faststart",
		"-y", outputFile,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "ffmpeg.cfg", 300*time.Second); err != nil {
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
