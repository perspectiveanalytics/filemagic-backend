package converter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

// skipMetadataKeys are exiftool keys that are always present (file system info)
// and not useful to show as "metadata".
var skipMetadataKeys = map[string]bool{
	"SourceFile":    true,
	"FileName":      true,
	"Directory":     true,
	"FileSize":      true,
	"FileModifyDate": true,
	"FileAccessDate": true,
	"FileInodeChangeDate": true,
	"FilePermissions": true,
	"FileType":       true,
	"FileTypeExtension": true,
	"MIMEType":       true,
	"ExifToolVersion": true,
}

// extractMetadata runs exiftool -json on the file and returns parsed metadata,
// filtering out file-system-level keys.
func extractMetadata(ctx context.Context, runner *NsjailRunner, inputFile, jobDir string) (map[string]any, error) {
	cmd := []string{
		"/usr/bin/exiftool",
		"-fast2",
		"-json",
		"-G",
		inputFile,
	}

	output, err := runner.RunWithOutput(ctx, cmd, jobDir, "metadata.cfg", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("metadata extraction failed: %w", err)
	}

	var results []map[string]any
	if err := json.Unmarshal(output, &results); err != nil {
		slog.Warn("failed to parse exiftool JSON", "error", err, "output", string(output))
		return nil, nil
	}

	if len(results) == 0 {
		return nil, nil
	}

	// Filter out file-system keys
	metadata := make(map[string]any)
	for k, v := range results[0] {
		// Strip the group prefix for the skip check (e.g. "File:FileName" → "FileName")
		baseKey := k
		if idx := strings.LastIndex(k, ":"); idx >= 0 {
			baseKey = k[idx+1:]
		}
		if skipMetadataKeys[baseKey] {
			continue
		}
		metadata[k] = v
	}

	return metadata, nil
}

// MetadataRemover

type MetadataRemover struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewMetadataRemover(cfg *config.Config, runner *NsjailRunner) *MetadataRemover {
	return &MetadataRemover{cfg: cfg, runner: runner}
}

func (c *MetadataRemover) Type() queue.ConversionType {
	return queue.ConversionMetadataRemove
}

func (c *MetadataRemover) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	ext := strings.ToLower(filepath.Ext(inputFile))
	if ext == "" {
		ext = ".bin"
	}

	// Capture metadata before removal
	metadata, err := extractMetadata(ctx, c.runner, inputFile, jobDir)
	if err != nil {
		slog.Warn("failed to extract metadata before removal", "error", err)
		// Continue with removal even if extraction fails
	}
	if metadata != nil {
		job.Metadata = metadata
	}

	outputFile := filepath.Join(outputDir, "output"+ext)

	// Copy input to output dir (exiftool works in-place)
	if err := copyFile(inputFile, outputFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	cmd := []string{
		"/usr/bin/exiftool",
		"-all=",
		"-overwrite_original",
		outputFile,
	}

	err = c.runner.Run(ctx, cmd, jobDir, "metadata.cfg", 30*time.Second)
	if err != nil {
		return fmt.Errorf("metadata removal failed: %w", err)
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()

	return nil
}

// MetadataInspector

type MetadataInspector struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewMetadataInspector(cfg *config.Config, runner *NsjailRunner) *MetadataInspector {
	return &MetadataInspector{cfg: cfg, runner: runner}
}

func (c *MetadataInspector) Type() queue.ConversionType {
	return queue.ConversionMetadataInspect
}

func (c *MetadataInspector) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, _, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	metadata, err := extractMetadata(ctx, c.runner, job.InputPath, jobDir)
	if err != nil {
		return err
	}

	job.Metadata = metadata

	// No output file for inspect-only mode — we set OutputPath to input
	// so the job completes successfully (download won't be used).
	job.OutputPath = job.InputPath
	job.OutputSize = job.InputSize

	return nil
}
