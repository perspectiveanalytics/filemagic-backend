package converter

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxImageDimension is the maximum allowed width or height in pixels.
	// Images larger than this are rejected to prevent decompression bomb DoS.
	// 16384 = 16K pixels — covers all reasonable use cases including 8K photos.
	MaxImageDimension = 16384

	// MaxImagePixels is the maximum total pixel count (width * height).
	// 100 megapixels: a 10000x10000 image uses ~300 MB when decoded (RGB).
	MaxImagePixels = 100_000_000
)

// ValidateImageDimensions reads image dimensions using vipsheader inside nsjail
// and rejects images that exceed safety limits. This prevents decompression bombs
// where a small compressed file decodes to a multi-gigabyte pixel buffer.
//
// Must be called AFTER PrepareJobDir but BEFORE the actual conversion.
func ValidateImageDimensions(ctx context.Context, runner *NsjailRunner, inputFile, jobDir, nsjailCfg string) error {
	dimBytes, err := runner.RunWithOutput(ctx,
		[]string{"/usr/bin/vipsheader", "-f", "width", "-f", "height", inputFile},
		jobDir, nsjailCfg, 10*time.Second)
	if err != nil {
		slog.Warn("vipsheader dimension check failed",
			"error", err, "input", inputFile)
		return fmt.Errorf("failed to validate image dimensions")
	}

	width, height := parseVipsDimensions(dimBytes)
	if width <= 0 || height <= 0 {
		width, height, err = readVipsDimensionsSeparately(ctx, runner, inputFile, jobDir, nsjailCfg)
		if err != nil {
			return err
		}
		if width <= 0 || height <= 0 {
			return fmt.Errorf("failed to parse image dimensions")
		}
	}

	slog.Info("image dimension check", "width", width, "height", height,
		"pixels", width*height)

	if width > MaxImageDimension || height > MaxImageDimension {
		return fmt.Errorf("image dimensions %dx%d exceed maximum allowed %d pixels per side",
			width, height, MaxImageDimension)
	}

	if int64(width)*int64(height) > MaxImagePixels {
		return fmt.Errorf("image pixel count %d exceeds maximum allowed %d megapixels",
			width*height, MaxImagePixels/1_000_000)
	}

	return nil
}

func readVipsDimensionsSeparately(ctx context.Context, runner *NsjailRunner, inputFile, jobDir, nsjailCfg string) (int, int, error) {
	width, err := readVipsDimensionField(ctx, runner, inputFile, jobDir, nsjailCfg, "width")
	if err != nil {
		return 0, 0, err
	}

	height, err := readVipsDimensionField(ctx, runner, inputFile, jobDir, nsjailCfg, "height")
	if err != nil {
		return 0, 0, err
	}

	return width, height, nil
}

func readVipsDimensionField(ctx context.Context, runner *NsjailRunner, inputFile, jobDir, nsjailCfg, field string) (int, error) {
	out, err := runner.RunWithOutput(ctx,
		[]string{"/usr/bin/vipsheader", "-f", field, inputFile},
		jobDir, nsjailCfg, 10*time.Second)
	if err != nil {
		slog.Warn("vipsheader dimension field check failed",
			"field", field, "error", err, "input", inputFile)
		return 0, fmt.Errorf("failed to validate image dimensions")
	}

	return parseVipsDimensionField(out), nil
}

func parseVipsDimensions(out []byte) (int, int) {
	var width, height int
	fmt.Fscan(strings.NewReader(strings.TrimSpace(string(out))), &width, &height)
	return width, height
}

func parseVipsDimensionField(out []byte) int {
	value, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return value
}
