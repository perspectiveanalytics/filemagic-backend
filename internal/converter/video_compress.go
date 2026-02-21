package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type VideoCompressor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewVideoCompressor(cfg *config.Config, runner *NsjailRunner) *VideoCompressor {
	return &VideoCompressor{cfg: cfg, runner: runner}
}

func (c *VideoCompressor) Type() queue.ConversionType {
	return queue.ConversionVideoCompress
}

func (c *VideoCompressor) Convert(ctx context.Context, job *queue.Job) error {
	mode, _ := job.Options["mode"].(string)
	if mode == "" {
		mode = "quality"
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.mp4")

	switch mode {
	case "targetSize":
		targetSize, _ := job.Options["targetSize"].(float64)
		if targetSize <= 0 || targetSize > 200<<20 {
			return fmt.Errorf("invalid target size")
		}
		if err := c.compressToTargetSize(ctx, jobDir, inputFile, outputFile, int64(targetSize)); err != nil {
			return err
		}
	default:
		quality, _ := job.Options["quality"].(string)
		if err := c.compressWithQuality(ctx, jobDir, inputFile, outputFile, quality); err != nil {
			return err
		}
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}

func (c *VideoCompressor) compressWithQuality(ctx context.Context, jobDir, inputFile, outputFile, quality string) error {
	crf := "23"
	switch quality {
	case "low":
		crf = "28"
	case "high":
		crf = "18"
	}

	cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", inputFile,
		"-c:v", "libx264", "-crf", crf,
		"-preset", "medium",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-y", outputFile,
	}

	return c.runner.Run(ctx, cmd, jobDir, "ffmpeg.cfg", 300*time.Second)
}

func (c *VideoCompressor) compressToTargetSize(ctx context.Context, jobDir, inputFile, outputFile string, targetBytes int64) error {
	probeCmd := []string{
		"/usr/bin/ffprobe",
		"-protocol_whitelist", "file,pipe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputFile,
	}
	durationOut, err := c.runner.RunWithOutput(ctx, probeCmd, jobDir, "ffmpeg.cfg", 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to probe video duration: %w", err)
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationOut)), 64)
	if err != nil || duration <= 0 {
		return fmt.Errorf("failed to parse video duration")
	}

	audioBitrate := int64(128000)
	videoBitrate := (targetBytes * 8) / int64(duration) - audioBitrate
	if videoBitrate < 100000 {
		videoBitrate = 100000
	}

	bitrateStr := fmt.Sprintf("%d", videoBitrate)
	passLogFile := filepath.Join(filepath.Dir(outputFile), "ffmpeg2pass")

	pass1Cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", inputFile,
		"-c:v", "libx264", "-b:v", bitrateStr,
		"-pass", "1", "-passlogfile", passLogFile,
		"-preset", "medium",
		"-an", "-f", "null", "/dev/null",
		"-y",
	}
	if err := c.runner.Run(ctx, pass1Cmd, jobDir, "ffmpeg.cfg", 300*time.Second); err != nil {
		return fmt.Errorf("2-pass encoding pass 1 failed: %w", err)
	}

	pass2Cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", inputFile,
		"-c:v", "libx264", "-b:v", bitrateStr,
		"-pass", "2", "-passlogfile", passLogFile,
		"-preset", "medium",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-y", outputFile,
	}
	if err := c.runner.Run(ctx, pass2Cmd, jobDir, "ffmpeg.cfg", 300*time.Second); err != nil {
		return fmt.Errorf("2-pass encoding pass 2 failed: %w", err)
	}

	return nil
}
