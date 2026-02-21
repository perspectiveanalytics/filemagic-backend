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

type VideoToGifConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewVideoToGifConverter(cfg *config.Config, runner *NsjailRunner) *VideoToGifConverter {
	return &VideoToGifConverter{cfg: cfg, runner: runner}
}

func (c *VideoToGifConverter) Type() queue.ConversionType {
	return queue.ConversionVideoToGif
}

func (c *VideoToGifConverter) Convert(ctx context.Context, job *queue.Job) error {
	startTime, _ := job.Options["startTime"].(float64)
	duration, _ := job.Options["duration"].(float64)
	fps, _ := job.Options["fps"].(float64)
	maxWidth, _ := job.Options["maxWidth"].(float64)
	speed, _ := job.Options["speed"].(float64)

	if duration <= 0 || duration > 15 {
		duration = 5
	}
	if fps < 5 {
		fps = 10
	}
	if fps > 15 {
		fps = 15
	}
	if maxWidth < 160 {
		maxWidth = 320
	}
	if maxWidth > 640 {
		maxWidth = 640
	}
	if startTime < 0 {
		startTime = 0
	}
	if speed < 0.25 || speed > 3 {
		speed = 1
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	outputFile := filepath.Join(outputDir, "output.gif")

	// Build filter: optional speed change via setpts, then fps, scale, palette
	var vfParts string
	if speed != 1 {
		// setpts=PTS/speed makes video faster (speed>1) or slower (speed<1)
		vfParts = fmt.Sprintf("setpts=PTS/%.2f,", speed)
	}
	vf := fmt.Sprintf("%sfps=%d,scale=%d:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse",
		vfParts, int(fps), int(maxWidth))

	cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-ss", fmt.Sprintf("%.2f", startTime),
		"-t", fmt.Sprintf("%.2f", duration),
		"-i", job.InputPath,
		"-vf", vf,
		"-y", outputFile,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "ffmpeg.cfg", 180*time.Second); err != nil {
		return err
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	if info.Size() > 20<<20 {
		return fmt.Errorf("generated GIF is too large (%d MB), try shorter duration, lower FPS, or smaller width", info.Size()/(1<<20))
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}
