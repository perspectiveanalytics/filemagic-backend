package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type ImageConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewImageConverter(cfg *config.Config, runner *NsjailRunner) *ImageConverter {
	return &ImageConverter{cfg: cfg, runner: runner}
}

func (c *ImageConverter) Type() queue.ConversionType {
	return queue.ConversionImageFormat
}

var validOutputFormats = map[string]bool{
	"jpg":  true,
	"png":  true,
	"webp": true,
	"tiff": true,
	"ico":  true,
	"pdf":  true,
}

func (c *ImageConverter) Convert(ctx context.Context, job *queue.Job) error {
	outputFormat, ok := job.Options["outputFormat"].(string)
	if !ok {
		return fmt.Errorf("missing outputFormat option")
	}

	outputFormat = strings.ToLower(outputFormat)
	if !validOutputFormats[outputFormat] {
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	// Copy hardened ImageMagick policy so vips can use magickload fallback
	// for formats like BMP that vips delegates to ImageMagick.
	if err := c.setupMagickPolicy(jobDir); err != nil {
		return err
	}

	// Reject decompression bombs: check pixel dimensions before processing
	if err := ValidateImageDimensions(ctx, c.runner, job.InputPath, jobDir, "image.cfg"); err != nil {
		return err
	}

	inputFile := job.InputPath

	// Pre-process: crop if crop coordinates are specified
	inputFile, err = c.applyCrop(ctx, job, jobDir, inputFile, outputDir)
	if err != nil {
		return err
	}

	// Pre-process: rotate if rotation angle is specified
	inputFile, err = c.applyRotation(ctx, job, jobDir, inputFile, outputDir)
	if err != nil {
		return err
	}

	// Pre-process: watermark if text is specified
	inputFile, err = c.applyWatermark(ctx, job, jobDir, inputFile, outputDir)
	if err != nil {
		return err
	}

	outputExt := "." + outputFormat
	outputFile := filepath.Join(outputDir, "output"+outputExt)

	// ICO requires a two-step process: vips decode → ImageMagick ICO packaging
	if outputFormat == "ico" {
		return c.convertToICO(ctx, job, jobDir, inputFile, outputFile, outputDir)
	}

	// PDF: vips decode → intermediate PNG → ImageMagick PDF
	if outputFormat == "pdf" {
		return c.convertToPDF(ctx, job, jobDir, inputFile, outputFile, outputDir)
	}

	// Check for resize dimensions
	resizeW, _ := job.Options["resizeWidth"].(float64)
	resizeH, _ := job.Options["resizeHeight"].(float64)

	if int(resizeW) > 0 || int(resizeH) > 0 {
		return c.convertWithResize(ctx, job, jobDir, inputFile, outputFile, outputFormat, int(resizeW), int(resizeH))
	}

	var cmd []string
	switch outputFormat {
	case "jpg":
		cmd = []string{
			"/usr/bin/vips", "jpegsave", inputFile, outputFile,
			"--Q", "90",
		}
	case "png":
		cmd = []string{
			"/usr/bin/vips", "pngsave", inputFile, outputFile,
			"--compression", "6",
		}
	case "webp":
		cmd = []string{
			"/usr/bin/vips", "webpsave", inputFile, outputFile,
			"--Q", "85",
		}
	case "tiff":
		cmd = []string{
			"/usr/bin/vips", "tiffsave", inputFile, outputFile,
			"--compression", "lzw",
		}
	}

	err = c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second)
	if err != nil {
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

// applyCrop runs "vips crop" if crop coordinates are present in the options.
// Returns the (possibly updated) input file path.
func (c *ImageConverter) applyCrop(ctx context.Context, job *queue.Job, jobDir, inputFile, outputDir string) (string, error) {
	cropX, xOk := job.Options["cropX"].(float64)
	cropY, yOk := job.Options["cropY"].(float64)
	cropW, wOk := job.Options["cropWidth"].(float64)
	cropH, hOk := job.Options["cropHeight"].(float64)

	if !xOk || !yOk || !wOk || !hOk {
		return inputFile, nil
	}

	x, y, w, h := int(cropX), int(cropY), int(cropW), int(cropH)
	if w <= 0 || h <= 0 {
		return "", fmt.Errorf("invalid crop dimensions: %dx%d", w, h)
	}
	if x < 0 || y < 0 {
		return "", fmt.Errorf("invalid crop offset: %d,%d", x, y)
	}

	croppedFile := filepath.Join(outputDir, "cropped.png")
	cmd := []string{
		"/usr/bin/vips", "crop", inputFile, croppedFile,
		fmt.Sprintf("%d", x),
		fmt.Sprintf("%d", y),
		fmt.Sprintf("%d", w),
		fmt.Sprintf("%d", h),
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
		return "", fmt.Errorf("crop failed: %w", err)
	}

	return croppedFile, nil
}

// applyRotation runs "vips rot" if a rotation angle (90, 180, 270) is present in the options.
// Returns the (possibly updated) input file path.
func (c *ImageConverter) applyRotation(ctx context.Context, job *queue.Job, jobDir, inputFile, outputDir string) (string, error) {
	angle, ok := job.Options["rotation"].(float64)
	if !ok {
		return inputFile, nil
	}

	var vipsAngle string
	switch int(angle) {
	case 90:
		vipsAngle = "d90"
	case 180:
		vipsAngle = "d180"
	case 270:
		vipsAngle = "d270"
	default:
		return "", fmt.Errorf("invalid rotation angle: %d (must be 90, 180, or 270)", int(angle))
	}

	rotatedFile := filepath.Join(outputDir, "rotated.v")
	cmd := []string{
		"/usr/bin/vips", "rot", inputFile, rotatedFile, vipsAngle,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
		return "", fmt.Errorf("rotation failed: %w", err)
	}

	return rotatedFile, nil
}

// applyWatermark uses ImageMagick "convert -annotate" if watermark text is present in the options.
// Returns the (possibly updated) input file path.
func (c *ImageConverter) applyWatermark(ctx context.Context, job *queue.Job, jobDir, inputFile, outputDir string) (string, error) {
	text, ok := job.Options["watermarkText"].(string)
	if !ok || strings.TrimSpace(text) == "" {
		return inputFile, nil
	}

	// Validate text: max 100 chars, printable only, no @ prefix
	// (ImageMagick interprets @filename as "read text from file")
	if strings.HasPrefix(text, "@") {
		return "", fmt.Errorf("watermark text must not start with @")
	}
	if len([]rune(text)) > 100 {
		return "", fmt.Errorf("watermark text too long (max 100 characters)")
	}
	for _, r := range text {
		if !unicode.IsPrint(r) && r != ' ' {
			return "", fmt.Errorf("watermark text contains invalid characters")
		}
	}

	// Parse position → ImageMagick gravity
	position, _ := job.Options["watermarkPosition"].(string)
	gravity := "SouthEast"
	switch position {
	case "top-left":
		gravity = "NorthWest"
	case "top-right":
		gravity = "NorthEast"
	case "bottom-left":
		gravity = "SouthWest"
	case "bottom-right":
		gravity = "SouthEast"
	case "center":
		gravity = "Center"
	}

	// Parse font size (12–120, default 24)
	fontSize := 24
	if v, ok := job.Options["watermarkSize"].(float64); ok {
		fontSize = int(v)
		if fontSize < 12 {
			fontSize = 12
		} else if fontSize > 120 {
			fontSize = 120
		}
	}

	// Parse color (whitelist: #ffffff or #000000)
	color := "#ffffff"
	if v, ok := job.Options["watermarkColor"].(string); ok {
		if v == "#000000" {
			color = "#000000"
		}
	}

	// Parse opacity (10–100, default 50) → alpha 0.0–1.0
	opacity := 50
	if v, ok := job.Options["watermarkOpacity"].(float64); ok {
		opacity = int(v)
		if opacity < 10 {
			opacity = 10
		} else if opacity > 100 {
			opacity = 100
		}
	}

	// Convert hex color + opacity to rgba
	var r, g, b int
	fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b)
	alpha := float64(opacity) / 100.0
	fill := fmt.Sprintf("rgba(%d,%d,%d,%g)", r, g, b, alpha)

	// Scale font size and offset proportionally to image dimensions.
	// The UI slider (12–120) is calibrated for a ~1000px reference image;
	// on a 4624px image fontSize=24 would be invisible without scaling.
	dimOut, derr := c.runner.RunWithOutput(ctx,
		[]string{"/usr/bin/identify", "-format", "%w %h", inputFile},
		jobDir, "image_to_pdf.cfg", 10*time.Second)
	if derr == nil {
		var imgW, imgH int
		fmt.Sscanf(strings.TrimSpace(string(dimOut)), "%d %d", &imgW, &imgH)
		if imgW > 0 && imgH > 0 {
			maxDim := imgW
			if imgH > maxDim {
				maxDim = imgH
			}
			fontSize = fontSize * maxDim / 1000
			if fontSize < 12 {
				fontSize = 12
			}
		}
	}

	offset := 20
	if fontSize > 40 {
		offset = fontSize / 2
	}

	watermarkedFile := filepath.Join(outputDir, "watermarked.png")
	cmd := []string{
		"/usr/bin/convert", inputFile,
		"-font", "Liberation-Sans",
		"-pointsize", fmt.Sprintf("%d", fontSize),
		"-fill", fill,
		"-gravity", gravity,
		"-annotate", fmt.Sprintf("+%d+%d", offset, offset), text,
		watermarkedFile,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "image_to_pdf.cfg", 120*time.Second); err != nil {
		return "", fmt.Errorf("watermark failed: %w", err)
	}

	return watermarkedFile, nil
}

// convertWithResize uses vipsthumbnail (aspect-preserving) or vips resize (exact stretch) to resize.
func (c *ImageConverter) convertWithResize(ctx context.Context, job *queue.Job, jobDir, inputFile, outputFile, format string, w, h int) error {
	keepAspect, _ := job.Options["keepAspect"].(bool)
	// Default to true if not specified (backwards compat)
	if _, ok := job.Options["keepAspect"]; !ok {
		keepAspect = true
	}

	// If only one dimension or keepAspect=true, use vipsthumbnail (bounding box)
	if keepAspect || w <= 0 || h <= 0 {
		size := ""
		if w > 0 && h > 0 {
			size = fmt.Sprintf("%dx%d", w, h)
		} else if w > 0 {
			size = fmt.Sprintf("%d", w)
		} else {
			size = fmt.Sprintf("x%d", h)
		}

		cmd := []string{
			"/usr/bin/vipsthumbnail", inputFile,
			"--size", size,
			"-o", formatOutputArg(outputFile, format),
		}

		if err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return err
		}
	} else {
		// Exact resize (may stretch): use vips resize with separate scale factors
		// Step 1: Get input dimensions
		wBytes, err := c.runner.RunWithOutput(ctx, []string{"/usr/bin/vipsheader", "-f", "width", inputFile}, jobDir, "image.cfg", 10*time.Second)
		if err != nil {
			return fmt.Errorf("failed to read input width: %w", err)
		}
		hBytes, err := c.runner.RunWithOutput(ctx, []string{"/usr/bin/vipsheader", "-f", "height", inputFile}, jobDir, "image.cfg", 10*time.Second)
		if err != nil {
			return fmt.Errorf("failed to read input height: %w", err)
		}

		var inputW, inputH int
		fmt.Sscanf(strings.TrimSpace(string(wBytes)), "%d", &inputW)
		fmt.Sscanf(strings.TrimSpace(string(hBytes)), "%d", &inputH)
		if inputW <= 0 || inputH <= 0 {
			return fmt.Errorf("invalid input dimensions: %dx%d", inputW, inputH)
		}

		hscale := float64(w) / float64(inputW)
		vscale := float64(h) / float64(inputH)

		// Step 2: Resize to intermediate .v file (vips resize outputs vips native format)
		resizedFile := filepath.Join(filepath.Dir(outputFile), "resized.v")
		resizeCmd := []string{
			"/usr/bin/vips", "resize", inputFile, resizedFile,
			fmt.Sprintf("%f", hscale),
			"--vscale", fmt.Sprintf("%f", vscale),
		}
		if err := c.runner.Run(ctx, resizeCmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return fmt.Errorf("resize failed: %w", err)
		}

		// Step 3: Convert to output format
		saveCmd := buildSaveCmd(resizedFile, outputFile, format)
		if err := c.runner.Run(ctx, saveCmd, jobDir, "image.cfg", 120*time.Second); err != nil {
			return fmt.Errorf("format conversion after resize failed: %w", err)
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

func formatOutputArg(outputFile, format string) string {
	switch format {
	case "jpg":
		return fmt.Sprintf("%s[Q=90]", outputFile)
	case "png":
		return fmt.Sprintf("%s[compression=6]", outputFile)
	case "webp":
		return fmt.Sprintf("%s[Q=85]", outputFile)
	case "tiff":
		return fmt.Sprintf("%s[compression=lzw]", outputFile)
	default:
		return outputFile
	}
}

func buildSaveCmd(inputFile, outputFile, format string) []string {
	switch format {
	case "jpg":
		return []string{"/usr/bin/vips", "jpegsave", inputFile, outputFile, "--Q", "90"}
	case "png":
		return []string{"/usr/bin/vips", "pngsave", inputFile, outputFile, "--compression", "6"}
	case "webp":
		return []string{"/usr/bin/vips", "webpsave", inputFile, outputFile, "--Q", "85"}
	case "tiff":
		return []string{"/usr/bin/vips", "tiffsave", inputFile, outputFile, "--compression", "lzw"}
	default:
		return []string{"/usr/bin/vips", "copy", inputFile, outputFile}
	}
}

func (c *ImageConverter) convertToPDF(ctx context.Context, job *queue.Job, jobDir, inputFile, outputFile, outputDir string) error {
	// Step 1: Convert to PNG via vips (intermediate format for ImageMagick)
	pngFile := filepath.Join(outputDir, "intermediate.png")
	vipsCmd := []string{
		"/usr/bin/vips", "pngsave", inputFile, pngFile,
		"--compression", "6",
	}
	if err := c.runner.Run(ctx, vipsCmd, jobDir, "image.cfg", 120*time.Second); err != nil {
		return fmt.Errorf("vips PNG conversion failed: %w", err)
	}

	// Step 2: Use ImageMagick to create PDF
	pdfCmd := []string{
		"/usr/bin/convert", pngFile, outputFile,
	}
	if err := c.runner.Run(ctx, pdfCmd, jobDir, "image_to_pdf.cfg", 30*time.Second); err != nil {
		return fmt.Errorf("ImageMagick PDF conversion failed: %w", err)
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}

// setupMagickPolicy copies the hardened ImageMagick policy.xml into the job
// directory so vips can use the magickload fallback (for BMP, etc.) with
// restricted ImageMagick capabilities.
func (c *ImageConverter) setupMagickPolicy(jobDir string) error {
	policyDir := filepath.Join(jobDir, "magick-config")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		return fmt.Errorf("failed to create policy dir: %w", err)
	}
	policySrc := filepath.Join(c.cfg.NsjailConfigDir, "imagemagick-policy.xml")
	if err := copyFile(policySrc, filepath.Join(policyDir, "policy.xml")); err != nil {
		return fmt.Errorf("failed to copy ImageMagick policy: %w", err)
	}
	return nil
}

func (c *ImageConverter) convertToICO(ctx context.Context, job *queue.Job, jobDir, inputFile, outputFile, outputDir string) error {
	// Step 1: Convert to PNG via vips (intermediate format for ImageMagick)
	pngFile := filepath.Join(outputDir, "intermediate.png")
	vipsCmd := []string{
		"/usr/bin/vips", "pngsave", inputFile, pngFile,
		"--compression", "6",
	}
	if err := c.runner.Run(ctx, vipsCmd, jobDir, "image.cfg", 120*time.Second); err != nil {
		return fmt.Errorf("vips PNG conversion failed: %w", err)
	}

	// Step 2: Use ImageMagick to create multi-size ICO favicon
	icoCmd := []string{
		"/usr/bin/convert", pngFile,
		"-define", "icon:auto-resize=256,128,64,48,32,16",
		outputFile,
	}
	if err := c.runner.Run(ctx, icoCmd, jobDir, "image_to_pdf.cfg", 30*time.Second); err != nil {
		return fmt.Errorf("ImageMagick ICO conversion failed: %w", err)
	}

	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()
	return nil
}
