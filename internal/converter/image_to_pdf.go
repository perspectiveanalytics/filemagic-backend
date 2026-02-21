package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type ImageToPDFConverter struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewImageToPDFConverter(cfg *config.Config, runner *NsjailRunner) *ImageToPDFConverter {
	return &ImageToPDFConverter{cfg: cfg, runner: runner}
}

func (c *ImageToPDFConverter) Type() queue.ConversionType {
	return queue.ConversionImageToPDF
}

func (c *ImageToPDFConverter) Convert(ctx context.Context, job *queue.Job) error {
	fileNames, err := getFileList(job, 1)
	if err != nil {
		return err
	}

	inputDir := job.InputPath
	jobDir := filepath.Dir(inputDir)
	outputDir := filepath.Join(jobDir, "output")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	// Copy hardened ImageMagick policy into job directory so nsjail can
	// expose it at /work/magick-config/policy.xml via MAGICK_CONFIGURE_PATH.
	policyDir := filepath.Join(jobDir, "magick-config")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		return fmt.Errorf("failed to create policy dir: %w", err)
	}
	policySrc := filepath.Join(c.cfg.NsjailConfigDir, "imagemagick-policy.xml")
	if err := copyFile(policySrc, filepath.Join(policyDir, "policy.xml")); err != nil {
		return fmt.Errorf("failed to copy ImageMagick policy: %w", err)
	}

	outputFile := filepath.Join(outputDir, "output.pdf")

	cmd := []string{"/usr/bin/convert"}
	for _, name := range fileNames {
		cmd = append(cmd, filepath.Join(inputDir, name))
	}
	cmd = append(cmd, outputFile)

	err = c.runner.Run(ctx, cmd, jobDir, "image_to_pdf.cfg", 120*time.Second)
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
