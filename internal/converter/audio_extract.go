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

type AudioExtractor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewAudioExtractor(cfg *config.Config, runner *NsjailRunner) *AudioExtractor {
	return &AudioExtractor{cfg: cfg, runner: runner}
}

func (c *AudioExtractor) Type() queue.ConversionType {
	return queue.ConversionAudioExtract
}

var validAudioFormats = map[string]bool{
	"mp3":  true,
	"wav":  true,
	"flac": true,
	"aac":  true,
}

func (c *AudioExtractor) Convert(ctx context.Context, job *queue.Job) error {
	outputFormat, ok := job.Options["outputFormat"].(string)
	if !ok {
		outputFormat = "mp3"
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

	// Probe for audio streams before attempting extraction
	probeCmd := []string{
		"/usr/bin/ffprobe",
		"-protocol_whitelist", "file,pipe",
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputFile,
	}
	probeOut, err := c.runner.RunWithOutput(ctx, probeCmd, jobDir, "ffmpeg.cfg", 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to probe file: %w", err)
	}
	if strings.TrimSpace(string(probeOut)) == "" {
		return fmt.Errorf("no audio stream found in file")
	}

	ext := outputFormat
	if outputFormat == "aac" {
		ext = "m4a" // AAC in M4A container
	}
	outputFile := filepath.Join(outputDir, "output."+ext)

	cmd := ffmpegCommand(inputFile, outputFile, outputFormat, true)

	if err := c.runner.Run(ctx, cmd, jobDir, "ffmpeg.cfg", 180*time.Second); err != nil {
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

// ffmpegCommand builds the ffmpeg command for audio conversion.
// If stripVideo is true, adds -vn to discard video streams.
func ffmpegCommand(input, output, format string, stripVideo bool) []string {
	cmd := []string{
		"/usr/bin/ffmpeg",
		"-nostdin",
		"-protocol_whitelist", "file,pipe",
		"-i", input,
	}
	if stripVideo {
		cmd = append(cmd, "-vn")
	}

	switch format {
	case "mp3":
		cmd = append(cmd, "-acodec", "libmp3lame", "-q:a", "2")
	case "wav":
		cmd = append(cmd, "-acodec", "pcm_s16le")
	case "flac":
		cmd = append(cmd, "-acodec", "flac")
	case "aac":
		cmd = append(cmd, "-acodec", "aac", "-b:a", "192k")
	}

	cmd = append(cmd, "-y", output)
	return cmd
}
