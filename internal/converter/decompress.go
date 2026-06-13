package converter

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
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

	if err := c.validateArchiveListing(ctx, jobDir, job.InputPath, password); err != nil {
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
	maxTotalSize := queue.MaxOutputSize

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
			return fmt.Errorf("decompressed content exceeds %d MB limit", maxTotalSize/(1024*1024))
		}

		relPath, _ := filepath.Rel(outputDir, path)

		// Security: reject relative paths that escape upward
		if strings.HasPrefix(relPath, "..") || strings.Contains(relPath, string(filepath.Separator)+"..") || strings.Contains(relPath, "\\") {
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

func (c *Decompressor) validateArchiveListing(ctx context.Context, jobDir, inputPath, password string) error {
	cmd := []string{"/usr/bin/7z", "l", "-slt", inputPath}
	if password != "" {
		cmd = append(cmd, "-p"+password)
	}

	out, err := c.runner.RunWithOutput(ctx, cmd, jobDir, "archive.cfg", 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to inspect archive before extraction: %w", err)
	}
	return validate7zListing(out, 100, queue.MaxOutputSize)
}

type archiveListEntry struct {
	path    string
	size    int64
	isDir   bool
	hasPath bool
	hasSize bool
}

func validate7zListing(out []byte, maxFiles int, maxTotalSize int64) error {
	var current archiveListEntry
	var inEntries bool
	var files int
	var total int64

	flush := func() error {
		if !current.hasPath {
			current = archiveListEntry{}
			return nil
		}
		defer func() { current = archiveListEntry{} }()

		if current.isDir {
			return validateArchiveEntryPath(current.path)
		}
		if !current.hasSize {
			return fmt.Errorf("archive entry %q has no declared size", current.path)
		}
		if err := validateArchiveEntryPath(current.path); err != nil {
			return err
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("archive contains more than %d files", maxFiles)
		}
		total += current.size
		if total > maxTotalSize {
			return fmt.Errorf("decompressed content exceeds %d MB limit", maxTotalSize/(1024*1024))
		}
		return nil
	}

	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "----------" {
			inEntries = true
			current = archiveListEntry{}
			continue
		}
		if !inEntries {
			continue
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}

		key, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		switch key {
		case "Path":
			if current.hasPath {
				if err := flush(); err != nil {
					return err
				}
			}
			current.path = value
			current.hasPath = true
		case "Size":
			size, err := strconv.ParseInt(value, 10, 64)
			if err != nil || size < 0 {
				return fmt.Errorf("archive entry %q has invalid size", current.path)
			}
			current.size = size
			current.hasSize = true
		case "Folder":
			current.isDir = value == "+"
		case "Attributes":
			if archiveAttributesIsDir(value) {
				current.isDir = true
			}
		}
	}

	if err := flush(); err != nil {
		return err
	}
	if files == 0 {
		return fmt.Errorf("archive is empty")
	}
	return nil
}

func archiveAttributesIsDir(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value[0] == 'D'
}

func validateArchiveEntryPath(name string) error {
	if name == "" {
		return fmt.Errorf("archive contains an empty path")
	}
	if strings.ContainsAny(name, "\x00\\") {
		return fmt.Errorf("archive entry %q contains an unsafe path separator", name)
	}
	if len(name) >= 3 && name[1] == ':' && name[2] == '/' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) {
		return fmt.Errorf("archive entry %q is absolute", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("archive entry %q is absolute", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("archive entry %q escapes output directory", name)
		}
	}
	clean := path.Clean(name)
	if clean == "." {
		return nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("archive entry %q escapes output directory", name)
	}
	return nil
}
