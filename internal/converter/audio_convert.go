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

type AudioConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewAudioConverter(cfg *config.Config, runner *NsjailRunner) *AudioConverter {
	return &AudioConverter{cfg: cfg, runner: runner}
}

func (c *AudioConverter) Type() queue.ConversionType {
	return queue.ConversionAudioConvert
}

func (c *AudioConverter) Convert(ctx context.Context, job *queue.Job) error {
	outputFormat, ok := job.Options["outputFormat"].(string)
	if !ok {
		return fmt.Errorf("missing outputFormat option")
	}
	outputFormat = strings.ToLower(outputFormat)
	if !validAudioFormats[outputFormat] {
		return fmt.Errorf("unsupported audio format: %s", outputFormat)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	ext := outputFormat
	if outputFormat == "aac" {
		ext = "m4a"
	}
	outputFile := filepath.Join(outputDir, "output."+ext)

	cmd := ffmpegCommand(inputFile, outputFile, outputFormat, false)

	if err := c.runner.Run(ctx, cmd, jobDir, "ffmpeg.cfg", 120*time.Second); err != nil {
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
