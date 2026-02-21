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
	"github.com/perspectiveanalytics/filemagic-backend/internal/validator"
)

// Tessdata search paths: Arch uses /usr/share/tessdata,
// Ubuntu/Debian uses /usr/share/tesseract-ocr/{4.00,5}/tessdata.
var tessdataPaths = []string{
	"/usr/share/tessdata",
	"/usr/share/tesseract-ocr/5/tessdata",
	"/usr/share/tesseract-ocr/4.00/tessdata",
}

type OCRConverter struct {
	cfg         *config.Config
	runner      *NsjailRunner
	tessdataDir string
}

func NewOCRConverter(cfg *config.Config, runner *NsjailRunner) *OCRConverter {
	dir := detectTessdataDir()
	return &OCRConverter{cfg: cfg, runner: runner, tessdataDir: dir}
}

func detectTessdataDir() string {
	for _, p := range tessdataPaths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	return ""
}

func (c *OCRConverter) Type() queue.ConversionType {
	return queue.ConversionOCR
}

func (c *OCRConverter) Convert(ctx context.Context, job *queue.Job) error {
	languages := c.parseLanguages(job.Options)

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	ext := strings.ToLower(filepath.Ext(inputFile))

	// Preprocess if Tesseract can't read the format directly.
	// Preprocessed files go to outputDir (input is read-only in nsjail).
	tessInput, err := c.preprocess(ctx, jobDir, outputDir, inputFile, ext)
	if err != nil {
		return fmt.Errorf("preprocessing failed: %w", err)
	}

	// Tesseract appends .txt to the output base path
	outputBase := filepath.Join(outputDir, "output")
	langArg := strings.Join(languages, "+")

	cmd := []string{"/usr/bin/tesseract"}
	if c.tessdataDir != "" {
		cmd = append(cmd, "--tessdata-dir", c.tessdataDir)
	}
	cmd = append(cmd, tessInput, outputBase, "-l", langArg, "txt")

	err = c.runner.Run(ctx, cmd, jobDir, "ocr.cfg", 90*time.Second)
	if err != nil {
		return fmt.Errorf("OCR failed: %w", err)
	}

	outputFile := outputBase + ".txt"
	info, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("OCR output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = info.Size()

	return nil
}

// preprocess converts formats that Tesseract can't read directly.
// outputDir is used for intermediate files since input is read-only in nsjail.
func (c *OCRConverter) preprocess(ctx context.Context, jobDir, outputDir, inputFile, ext string) (string, error) {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".bmp":
		// Downsample to 150 DPI equivalent for faster OCR
		preprocessed := filepath.Join(outputDir, "preprocessed.png")
		cmd := []string{
			"/usr/bin/vips", "thumbnail", inputFile, preprocessed,
			"2048", "--height", "2048",
		}
		err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 30*time.Second)
		if err != nil {
			// Fall back to original if vips fails (e.g. already small)
			return inputFile, nil
		}
		return preprocessed, nil

	case ".heic", ".webp":
		preprocessed := filepath.Join(outputDir, "preprocessed.png")
		cmd := []string{
			"/usr/bin/vips", "thumbnail", inputFile, preprocessed,
			"2048", "--height", "2048",
		}
		err := c.runner.Run(ctx, cmd, jobDir, "image.cfg", 30*time.Second)
		if err != nil {
			return "", fmt.Errorf("image to PNG conversion failed: %w", err)
		}
		return preprocessed, nil

	case ".pdf":
		preprocessed := filepath.Join(outputDir, "preprocessed.tiff")
		cmd := []string{
			"/usr/bin/gs",
			"-dSAFER",
			"-dPARANOIDSAFER",
			"-dNOOUTERSAVE",
			"-dNEWPDF=true",
			"-dMaxBitmap=100000000",
			"-sDEVICE=tiffgray",
			"-r150",
			"-dNOPAUSE",
			"-dQUIET",
			"-dBATCH",
			fmt.Sprintf("-sOutputFile=%s", preprocessed),
			inputFile,
		}
		err := c.runner.Run(ctx, cmd, jobDir, "pdf.cfg", 60*time.Second)
		if err != nil {
			return "", fmt.Errorf("PDF to TIFF conversion failed: %w", err)
		}
		return preprocessed, nil

	default:
		return "", fmt.Errorf("unsupported file format for OCR: %s", ext)
	}
}

// parseLanguages extracts and validates the languages from job options.
func (c *OCRConverter) parseLanguages(options map[string]any) []string {
	raw, ok := options["languages"]
	if !ok {
		return []string{"eng"}
	}

	rawSlice, ok := raw.([]interface{})
	if !ok {
		return []string{"eng"}
	}

	var languages []string
	for _, v := range rawSlice {
		if s, ok := v.(string); ok {
			if validator.ValidOCRLanguages[s] {
				languages = append(languages, s)
			}
		}
	}

	if len(languages) == 0 {
		return []string{"eng"}
	}
	if len(languages) > 1 {
		languages = languages[:1]
	}

	return languages
}
