package converter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type ImageCompressor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewImageCompressor(cfg *config.Config, runner *NsjailRunner) *ImageCompressor {
	return &ImageCompressor{cfg: cfg, runner: runner}
}

func (c *ImageCompressor) Type() queue.ConversionType {
	return queue.ConversionImageCompress
}

func (c *ImageCompressor) Convert(ctx context.Context, job *queue.Job) error {
	// Pre-process: crop if crop coordinates are specified
	if err := c.applyCrop(ctx, job); err != nil {
		return err
	}

	// Check if target size mode is requested
	if ts, ok := job.Options["targetSize"].(float64); ok && ts > 0 {
		return c.compressToTargetSize(ctx, job, int64(ts))
	}

	return c.compressWithQuality(ctx, job)
}

// applyCrop runs "vips crop" if crop coordinates are present in the options.
// It writes the cropped image to the output dir and updates job.InputPath
// so subsequent compress steps use the cropped version.
func (c *ImageCompressor) applyCrop(ctx context.Context, job *queue.Job) error {
	cropX, xOk := job.Options["cropX"].(float64)
	cropY, yOk := job.Options["cropY"].(float64)
	cropW, wOk := job.Options["cropWidth"].(float64)
	cropH, hOk := job.Options["cropHeight"].(float64)

	if !xOk || !yOk || !wOk || !hOk {
		return nil // No crop requested
	}

	x, y, w, h := int(cropX), int(cropY), int(cropW), int(cropH)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid crop dimensions: %dx%d", w, h)
	}
	if x < 0 || y < 0 {
		return fmt.Errorf("invalid crop offset: %d,%d", x, y)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(job.OriginalName))
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	croppedFile := filepath.Join(outputDir, "cropped"+ext)

	cmd := []string{
		"/usr/bin/vips", "crop", job.InputPath, croppedFile,
		fmt.Sprintf("%d", x),
		fmt.Sprintf("%d", y),
		fmt.Sprintf("%d", w),
		fmt.Sprintf("%d", h),
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
		return fmt.Errorf("crop failed: %w", err)
	}

	croppedInfo, err := os.Stat(croppedFile)
	if err != nil {
		return fmt.Errorf("cropped file not found: %w", err)
	}

	job.InputPath = croppedFile
	job.InputSize = croppedInfo.Size()
	return nil
}

// compressWithQuality is the original quality-based compression path.
func (c *ImageCompressor) compressWithQuality(ctx context.Context, job *queue.Job) error {
	quality := 80
	if q, ok := job.Options["quality"].(float64); ok {
		quality = int(q)
	}
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}

	maxWidth := 0
	if w, ok := job.Options["maxWidth"].(float64); ok {
		maxWidth = int(w)
	}

	maxHeight := 0
	if h, ok := job.Options["maxHeight"].(float64); ok {
		maxHeight = int(h)
	}

	// Exact resize dimensions take priority over max dimensions
	if rw, ok := job.Options["resizeWidth"].(float64); ok && int(rw) > 0 {
		maxWidth = int(rw)
	}
	if rh, ok := job.Options["resizeHeight"].(float64); ok && int(rh) > 0 {
		maxHeight = int(rh)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	// Reject decompression bombs before expensive processing
	if err := ValidateImageDimensions(ctx, c.runner, job.InputPath, jobDir, "image.cfg"); err != nil {
		return err
	}

	inputFile := job.InputPath
	ext := strings.ToLower(filepath.Ext(job.OriginalName))

	isJpeg := ext == ".jpg" || ext == ".jpeg"
	outputExt := ext
	if outputExt == ".jpeg" {
		outputExt = ".jpg"
	}
	outputFile := filepath.Join(outputDir, "output"+outputExt)

	var cmd []string

	if maxWidth > 0 || maxHeight > 0 {
		size := ""
		if maxWidth > 0 && maxHeight > 0 {
			size = fmt.Sprintf("%dx%d", maxWidth, maxHeight)
		} else if maxWidth > 0 {
			size = fmt.Sprintf("%d", maxWidth)
		} else {
			size = fmt.Sprintf("x%d", maxHeight)
		}

		if isJpeg {
			cmd = []string{
				"/usr/bin/vipsthumbnail", inputFile,
				"--size", size,
				"-o", fmt.Sprintf("%s[Q=%d]", outputFile, quality),
			}
		} else {
			cmd = []string{
				"/usr/bin/vipsthumbnail", inputFile,
				"--size", size,
				"-o", fmt.Sprintf("%s[compression=%d]", outputFile, 9-quality/12),
			}
		}
	} else {
		if isJpeg {
			cmd = []string{
				"/usr/bin/vips", "jpegsave", inputFile, outputFile,
				"--Q", fmt.Sprintf("%d", quality),
			}
		} else {
			compression := 9 - quality/12
			if compression < 0 {
				compression = 0
			}
			if compression > 9 {
				compression = 9
			}
			cmd = []string{
				"/usr/bin/vips", "pngsave", inputFile, outputFile,
				"--compression", fmt.Sprintf("%d", compression),
			}
		}
	}

	err = c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second)
	if err != nil {
		return err
	}

	outputInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	// If compression made the file bigger and no resize was requested, use the original
	if maxWidth == 0 && maxHeight == 0 && outputInfo.Size() >= job.InputSize && job.InputSize > 0 {
		slog.Info("compressed image is larger than original, using original",
			"inputSize", job.InputSize,
			"outputSize", outputInfo.Size(),
		)
		if err := copyFile(inputFile, outputFile); err != nil {
			return fmt.Errorf("failed to copy original: %w", err)
		}
		job.OutputPath = outputFile
		job.OutputSize = job.InputSize
		return nil
	}

	job.OutputPath = outputFile
	job.OutputSize = outputInfo.Size()

	return nil
}

// compressToTargetSize iteratively adjusts quality (JPEG) or resolution (PNG)
// to produce output close to but not exceeding targetSize bytes.
func (c *ImageCompressor) compressToTargetSize(ctx context.Context, job *queue.Job, targetSize int64) error {
	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	// Reject decompression bombs before expensive processing
	if err := ValidateImageDimensions(ctx, c.runner, job.InputPath, jobDir, "image.cfg"); err != nil {
		return err
	}

	inputFile := job.InputPath
	ext := strings.ToLower(filepath.Ext(job.OriginalName))
	isJpeg := ext == ".jpg" || ext == ".jpeg"

	outputExt := ext
	if outputExt == ".jpeg" {
		outputExt = ".jpg"
	}
	outputFile := filepath.Join(outputDir, "output"+outputExt)

	// If input is already under target, just copy it
	if job.InputSize > 0 && job.InputSize <= targetSize {
		if err := copyFile(inputFile, outputFile); err != nil {
			return fmt.Errorf("failed to copy original: %w", err)
		}
		job.OutputPath = outputFile
		job.OutputSize = job.InputSize
		return nil
	}

	if isJpeg {
		return c.jpegTargetSize(ctx, job, jobDir, inputFile, outputFile, targetSize)
	}
	return c.pngTargetSize(ctx, job, jobDir, inputFile, outputFile, targetSize)
}

// jpegTargetSize uses iterative quality adjustment to hit the target.
// Strategy: estimate initial quality from size ratio, then refine in 2-3 passes.
// If quality alone can't reach the target, falls back to resolution reduction.
func (c *ImageCompressor) jpegTargetSize(ctx context.Context, job *queue.Job, jobDir, inputFile, outputFile string, targetSize int64) error {
	const maxIterations = 4
	const minQ = 10
	const maxQ = 95

	// Estimate initial quality from the size ratio
	ratio := float64(targetSize) / float64(job.InputSize)
	quality := int(math.Sqrt(ratio) * 85) // sqrt because JPEG size ~ quality^2 roughly
	if quality < minQ {
		quality = minQ
	}
	if quality > maxQ {
		quality = maxQ
	}

	var bestQuality int
	var bestSize int64
	lastQuality := -1

	for i := 0; i < maxIterations; i++ {
		if quality == lastQuality {
			break
		}
		lastQuality = quality

		cmd := []string{
			"/usr/bin/vips", "jpegsave", inputFile, outputFile,
			"--Q", fmt.Sprintf("%d", quality),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return err
		}

		info, err := os.Stat(outputFile)
		if err != nil {
			return fmt.Errorf("output file not found: %w", err)
		}
		outSize := info.Size()

		slog.Info("jpeg target size iteration",
			"iteration", i+1,
			"quality", quality,
			"outputSize", outSize,
			"targetSize", targetSize,
		)

		if outSize <= targetSize {
			bestQuality = quality
			bestSize = outSize
			// Within 10% of target or last iteration — good enough
			if float64(targetSize-outSize)/float64(targetSize) < 0.10 || i == maxIterations-1 {
				break
			}
			// Try higher quality to get closer to target
			newQ := int(float64(quality) * math.Sqrt(float64(targetSize)/float64(outSize)))
			if newQ <= quality {
				newQ = quality + 2
			}
			if newQ > maxQ {
				newQ = maxQ
			}
			quality = newQ
		} else {
			// Over target — reduce quality proportionally
			newQ := int(float64(quality) * math.Sqrt(float64(targetSize)/float64(outSize)))
			if newQ >= quality {
				newQ = quality - 5
			}
			if newQ < minQ {
				newQ = minQ
			}
			quality = newQ
		}
	}

	// If quality alone couldn't reach the target, add resolution reduction
	if bestQuality == 0 {
		// First get the min-quality file size to calculate needed scale
		cmd := []string{
			"/usr/bin/vips", "jpegsave", inputFile, outputFile,
			"--Q", fmt.Sprintf("%d", minQ),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return err
		}
		info, err := os.Stat(outputFile)
		if err != nil {
			return fmt.Errorf("output file not found: %w", err)
		}
		minQSize := info.Size()

		if minQSize <= targetSize {
			// Min quality alone is enough after all
			job.OutputPath = outputFile
			job.OutputSize = minQSize
			return nil
		}

		// Need resolution reduction: scale by sqrt(target/minQSize)
		scale := math.Sqrt(float64(targetSize) / float64(minQSize))
		if scale > 0.95 {
			scale = 0.95
		}
		if scale < 0.05 {
			scale = 0.05
		}

		for i := 0; i < 3; i++ {
			cmd = []string{
				"/usr/bin/vips", "resize", inputFile,
				fmt.Sprintf("%s[Q=%d]", outputFile, minQ),
				fmt.Sprintf("%.4f", scale),
			}
			if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
				return err
			}
			info, err = os.Stat(outputFile)
			if err != nil {
				return fmt.Errorf("output file not found: %w", err)
			}
			outSize := info.Size()

			slog.Info("jpeg target size resize iteration",
				"iteration", i+1,
				"scale", fmt.Sprintf("%.2f", scale),
				"quality", minQ,
				"outputSize", outSize,
				"targetSize", targetSize,
			)

			if outSize <= targetSize || i == 2 {
				job.OutputPath = outputFile
				job.OutputSize = outSize
				return nil
			}
			scale *= math.Sqrt(float64(targetSize) / float64(outSize))
		}

		job.OutputPath = outputFile
		job.OutputSize = info.Size()
		return nil
	}

	// Re-compress at best quality if the last iteration wasn't it
	if bestSize == 0 {
		cmd := []string{
			"/usr/bin/vips", "jpegsave", inputFile, outputFile,
			"--Q", fmt.Sprintf("%d", bestQuality),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return err
		}
		info, err := os.Stat(outputFile)
		if err != nil {
			return fmt.Errorf("output file not found: %w", err)
		}
		bestSize = info.Size()
	}

	job.OutputPath = outputFile
	job.OutputSize = bestSize
	return nil
}

// pngTargetSize reduces resolution to hit the target since PNG is lossless.
// Uses "vips resize" with a float scale factor.
func (c *ImageCompressor) pngTargetSize(ctx context.Context, job *queue.Job, jobDir, inputFile, outputFile string, targetSize int64) error {
	// Calculate scale factor: PNG size roughly proportional to pixel count
	scale := math.Sqrt(float64(targetSize) / float64(job.InputSize))
	if scale >= 1.0 {
		scale = 0.9 // at least 10% reduction
	}
	if scale < 0.05 {
		scale = 0.05
	}

	const maxIterations = 3

	for i := 0; i < maxIterations; i++ {
		cmd := []string{
			"/usr/bin/vips", "resize", inputFile,
			fmt.Sprintf("%s[compression=9]", outputFile),
			fmt.Sprintf("%.4f", scale),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return err
		}

		info, err := os.Stat(outputFile)
		if err != nil {
			return fmt.Errorf("output file not found: %w", err)
		}
		outSize := info.Size()

		slog.Info("png target size iteration",
			"iteration", i+1,
			"scale", fmt.Sprintf("%.2f", scale),
			"outputSize", outSize,
			"targetSize", targetSize,
		)

		if outSize <= targetSize || i == maxIterations-1 {
			job.OutputPath = outputFile
			job.OutputSize = outSize
			return nil
		}

		// Adjust scale further
		scale *= math.Sqrt(float64(targetSize) / float64(outSize))
		if scale < 0.05 {
			scale = 0.05
		}
	}

	return nil // unreachable
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
