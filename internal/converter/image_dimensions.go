package converter

import (
	"context"
	"fmt"
	"log/slog"
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
	wBytes, err := runner.RunWithOutput(ctx,
		[]string{"/usr/bin/vipsheader", "-f", "width", inputFile},
		jobDir, nsjailCfg, 10*time.Second)
	if err != nil {
		// If vipsheader fails, the image is likely corrupted — let the
		// converter handle it (it will fail with a better error message).
		slog.Warn("vipsheader width check failed, skipping dimension validation",
			"error", err, "input", inputFile)
		return nil
	}

	hBytes, err := runner.RunWithOutput(ctx,
		[]string{"/usr/bin/vipsheader", "-f", "height", inputFile},
		jobDir, nsjailCfg, 10*time.Second)
	if err != nil {
		slog.Warn("vipsheader height check failed, skipping dimension validation",
			"error", err, "input", inputFile)
		return nil
	}

	var width, height int
	fmt.Sscanf(strings.TrimSpace(string(wBytes)), "%d", &width)
	fmt.Sscanf(strings.TrimSpace(string(hBytes)), "%d", &height)

	if width <= 0 || height <= 0 {
		// Could not parse dimensions — let the converter handle it
		return nil
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
