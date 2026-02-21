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

// imageExtensions that ImageMagick can convert to single-page PDFs.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
}

type PDFMerger struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFMerger(cfg *config.Config, runner *NsjailRunner) *PDFMerger {
	return &PDFMerger{cfg: cfg, runner: runner}
}

func (c *PDFMerger) Type() queue.ConversionType {
	return queue.ConversionPDFMerge
}

func (c *PDFMerger) Convert(ctx context.Context, job *queue.Job) error {
	fileNames, err := getFileList(job, 2)
	if err != nil {
		return err
	}

	inputDir := job.InputPath
	jobDir := filepath.Dir(inputDir)
	outputDir := filepath.Join(jobDir, "output")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	// Pre-convert any image files to single-page PDFs using ImageMagick.
	pdfFiles, err := c.convertImagesToPDF(ctx, fileNames, inputDir, jobDir)
	if err != nil {
		return err
	}

	outputFile := filepath.Join(outputDir, "output.pdf")

	cmd := []string{
		"/usr/bin/gs",
		"-dSAFER",
		"-dPARANOIDSAFER",
		"-dNOOUTERSAVE",
		"-dNEWPDF=true",
		"-dMaxBitmap=100000000",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dNOPAUSE",
		"-dQUIET",
		"-dBATCH",
		fmt.Sprintf("-sOutputFile=%s", outputFile),
	}

	for _, name := range pdfFiles {
		cmd = append(cmd, filepath.Join(inputDir, name))
	}

	err = c.runner.Run(ctx, cmd, jobDir, "pdf.cfg", 60*time.Second)
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

// convertImagesToPDF converts any image files in the list to single-page PDFs
// using ImageMagick. Returns the final list of filenames (all PDFs).
func (c *PDFMerger) convertImagesToPDF(ctx context.Context, fileNames []string, inputDir, jobDir string) ([]string, error) {
	hasImages := false
	for _, name := range fileNames {
		if imageExtensions[strings.ToLower(filepath.Ext(name))] {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return fileNames, nil
	}

	// Copy hardened ImageMagick policy (needed for convert).
	policyDir := filepath.Join(jobDir, "magick-config")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create policy dir: %w", err)
	}
	policySrc := filepath.Join(c.cfg.NsjailConfigDir, "imagemagick-policy.xml")
	if err := copyFile(policySrc, filepath.Join(policyDir, "policy.xml")); err != nil {
		return nil, fmt.Errorf("failed to copy ImageMagick policy: %w", err)
	}

	result := make([]string, 0, len(fileNames))
	for _, name := range fileNames {
		ext := strings.ToLower(filepath.Ext(name))
		if !imageExtensions[ext] {
			result = append(result, name)
			continue
		}

		// Convert image → single-page PDF in the same input directory.
		pdfName := strings.TrimSuffix(name, filepath.Ext(name)) + "_converted.pdf"
		cmd := []string{
			"/usr/bin/convert",
			filepath.Join(inputDir, name),
			filepath.Join(inputDir, pdfName),
		}
		if err := c.runner.Run(ctx, cmd, jobDir, "image_to_pdf.cfg", 30*time.Second); err != nil {
			return nil, fmt.Errorf("failed to convert image %s to PDF: %w", name, err)
		}
		result = append(result, pdfName)
	}

	return result, nil
}

func getFileList(job *queue.Job, minFiles int) ([]string, error) {
	filesRaw, ok := job.Options["files"]
	if !ok {
		return nil, fmt.Errorf("missing files list in options")
	}

	filesSlice, ok := filesRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid files list format")
	}

	if len(filesSlice) < minFiles {
		return nil, fmt.Errorf("at least %d file(s) required", minFiles)
	}

	fileNames := make([]string, 0, len(filesSlice))
	for _, f := range filesSlice {
		name, ok := f.(string)
		if !ok {
			return nil, fmt.Errorf("invalid file name in list")
		}
		// Prevent path traversal
		if filepath.Base(name) != name {
			return nil, fmt.Errorf("invalid file name: %s", name)
		}
		fileNames = append(fileNames, name)
	}

	return fileNames, nil
}
