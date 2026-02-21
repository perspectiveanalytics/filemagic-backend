package converter

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type FaviconGenerator struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewFaviconGenerator(cfg *config.Config, runner *NsjailRunner) *FaviconGenerator {
	return &FaviconGenerator{cfg: cfg, runner: runner}
}

func (c *FaviconGenerator) Type() queue.ConversionType {
	return queue.ConversionFavicon
}

type faviconSize struct {
	name string
	size int
}

var faviconSizes = []faviconSize{
	{"favicon-16x16.png", 16},
	{"favicon-32x32.png", 32},
	{"favicon-48x48.png", 48},
	{"apple-touch-icon.png", 180},
	{"android-chrome-192x192.png", 192},
	{"android-chrome-512x512.png", 512},
}

func (c *FaviconGenerator) Convert(ctx context.Context, job *queue.Job) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath

	// Step 1: Convert input to intermediate PNG via vips
	intermediatePng := filepath.Join(outputDir, "intermediate.png")
	vipsCmd := []string{
		"/usr/bin/vips", "pngsave", inputFile, intermediatePng,
		"--compression", "6",
	}
	if err := c.runner.Run(ctx, vipsCmd, jobDir, "image.cfg", 30*time.Second); err != nil {
		return fmt.Errorf("vips PNG conversion failed: %w", err)
	}

	// Step 2: Generate each favicon size via vipsthumbnail
	for _, fs := range faviconSizes {
		outPath := filepath.Join(outputDir, fs.name)
		size := fmt.Sprintf("%dx%d", fs.size, fs.size)
		cmd := []string{
			"/usr/bin/vipsthumbnail", intermediatePng,
			"--size", size,
			"-o", fmt.Sprintf("%s[compression=6]", outPath),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 30*time.Second); err != nil {
			return fmt.Errorf("failed to generate %s: %w", fs.name, err)
		}
	}

	// Step 3: Generate favicon.ico with ImageMagick (multi-size: 16, 32, 48)
	icoFile := filepath.Join(outputDir, "favicon.ico")
	icoCmd := []string{
		"/usr/bin/convert", intermediatePng,
		"-define", "icon:auto-resize=48,32,16",
		icoFile,
	}
	if err := c.runner.Run(ctx, icoCmd, jobDir, "image_to_pdf.cfg", 30*time.Second); err != nil {
		return fmt.Errorf("ImageMagick ICO generation failed: %w", err)
	}

	// Remove intermediate file
	os.Remove(intermediatePng)

	// Build file manifest
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("failed to read output directory: %w", err)
	}

	var files []map[string]any
	var totalSize int64
	index := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		totalSize += info.Size()
		mimeType := mime.TypeByExtension(filepath.Ext(entry.Name()))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		files = append(files, map[string]any{
			"name":  entry.Name(),
			"size":  info.Size(),
			"index": index,
			"type":  mimeType,
		})
		index++
	}

	if len(files) == 0 {
		return fmt.Errorf("no favicon files generated")
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
