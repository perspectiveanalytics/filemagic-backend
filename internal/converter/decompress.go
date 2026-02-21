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

type Decompressor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewDecompressor(cfg *config.Config, runner *NsjailRunner) *Decompressor {
	return &Decompressor{cfg: cfg, runner: runner}
}

func (c *Decompressor) Type() queue.ConversionType {
	return queue.ConversionDecompress
}

func (c *Decompressor) Convert(ctx context.Context, job *queue.Job) error {
	password, _ := job.Options["password"].(string)

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	cmd := []string{"/usr/bin/7z", "x", job.InputPath, "-o" + outputDir, "-y"}
	if password != "" {
		cmd = append(cmd, "-p"+password)
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "archive.cfg", 120*time.Second); err != nil {
		return err
	}

	var files []map[string]any
	var totalSize int64
	index := 0
	maxFiles := 100
	maxTotalSize := int64(200 << 20)

	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Security: reject symlinks to prevent escape from output directory
		if info.Mode()&os.ModeSymlink != 0 {
			os.Remove(path)
			return nil
		}

		if info.IsDir() {
			return nil
		}
		if index >= maxFiles {
			return fmt.Errorf("archive contains more than %d files", maxFiles)
		}

		// Security: verify extracted file is within outputDir (path traversal defense)
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("invalid file path: %w", err)
		}
		absOutputDir, _ := filepath.Abs(outputDir)
		if !strings.HasPrefix(absPath, absOutputDir+string(filepath.Separator)) && absPath != absOutputDir {
			return fmt.Errorf("path traversal detected: %s escapes output directory", path)
		}

		totalSize += info.Size()
		if totalSize > maxTotalSize {
			return fmt.Errorf("decompressed content exceeds 200 MB limit")
		}

		relPath, _ := filepath.Rel(outputDir, path)

		// Security: reject relative paths that escape upward
		if strings.HasPrefix(relPath, "..") || strings.Contains(relPath, string(filepath.Separator)+"..") {
			return fmt.Errorf("path traversal detected in archive entry: %s", relPath)
		}

		files = append(files, map[string]any{
			"name":  relPath,
			"size":  info.Size(),
			"index": index,
		})
		index++
		return nil
	})

	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("archive is empty")
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
