package converter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/scripts"
)

// Hard limits for PDF editor options to prevent abuse.
const (
	maxPages      = 500
	maxRotations  = 500
	maxRedactions = 200
	maxTextLen    = 200
	maxFontSize   = 200.0
	maxMargin     = 100.0
)

type PDFEditor struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewPDFEditor(cfg *config.Config, runner *NsjailRunner) *PDFEditor {
	return &PDFEditor{cfg: cfg, runner: runner}
}

func (c *PDFEditor) Type() queue.ConversionType {
	return queue.ConversionPDFEdit
}

func (c *PDFEditor) Convert(ctx context.Context, job *queue.Job) error {
	if err := validatePDFEditOptions(job.Options); err != nil {
		return fmt.Errorf("invalid options: %w", err)
	}

	jobDir, _, outputDir, err := PrepareJobDir(c.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputFile := job.InputPath
	outputFile := filepath.Join(outputDir, "output.pdf")

	// Write embedded Python script to job directory
	scriptPath := filepath.Join(jobDir, "editor.py")
	if err := os.WriteFile(scriptPath, scripts.PDFEditorScript, 0644); err != nil {
		return fmt.Errorf("failed to write editor script: %w", err)
	}

	// Write config JSON from job options
	configData, err := json.Marshal(job.Options)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	configPath := filepath.Join(jobDir, "config.json")
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	cmd := []string{
		"/usr/bin/python3",
		scriptPath,
		inputFile,
		outputFile,
		configPath,
	}

	if err := c.runner.Run(ctx, cmd, jobDir, "pymupdf.cfg", 60*time.Second); err != nil {
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

// validatePDFEditOptions enforces strict limits on user-supplied options
// before they reach the Python script.
func validatePDFEditOptions(opts map[string]any) error {
	if opts == nil {
		return nil
	}

	// Validate pages array
	if pages, ok := opts["pages"]; ok {
		arr, ok := pages.([]any)
		if !ok {
			return fmt.Errorf("pages must be an array")
		}
		if len(arr) > maxPages {
			return fmt.Errorf("pages exceeds maximum of %d", maxPages)
		}
		for _, p := range arr {
			n, ok := toFloat64(p)
			if !ok || n < 1 || n > maxPages || n != float64(int(n)) {
				return fmt.Errorf("invalid page number")
			}
		}
	}

	// Validate rotations map
	if rotations, ok := opts["rotations"]; ok {
		m, ok := rotations.(map[string]any)
		if !ok {
			return fmt.Errorf("rotations must be an object")
		}
		if len(m) > maxRotations {
			return fmt.Errorf("rotations exceeds maximum of %d", maxRotations)
		}
		for _, v := range m {
			angle, ok := toFloat64(v)
			if !ok {
				return fmt.Errorf("rotation angle must be a number")
			}
			if angle != 90 && angle != 180 && angle != 270 {
				return fmt.Errorf("rotation angle must be 90, 180, or 270")
			}
		}
	}

	// Validate watermark
	if wm, ok := opts["watermark"]; ok {
		m, ok := wm.(map[string]any)
		if !ok {
			return fmt.Errorf("watermark must be an object")
		}
		if text, ok := m["text"].(string); ok {
			if len(text) > maxTextLen {
				return fmt.Errorf("watermark text exceeds maximum of %d characters", maxTextLen)
			}
		}
		if err := validateFloat(m, "fontSize", 1, maxFontSize); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
		if err := validateFloat(m, "opacity", 0, 1); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
		if err := validateFloat(m, "rotation", -360, 360); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
		if err := validateColorArray(m, "color"); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
		if err := validatePosition(m, "position", []string{"center", "top-left", "top-right", "bottom-left", "bottom-right"}); err != nil {
			return fmt.Errorf("watermark: %w", err)
		}
	}

	// Validate page numbers
	if pn, ok := opts["pageNumbers"]; ok {
		m, ok := pn.(map[string]any)
		if !ok {
			return fmt.Errorf("pageNumbers must be an object")
		}
		if format, ok := m["format"].(string); ok {
			if len(format) > maxTextLen {
				return fmt.Errorf("pageNumbers format exceeds maximum of %d characters", maxTextLen)
			}
		}
		if err := validateFloat(m, "fontSize", 1, maxFontSize); err != nil {
			return fmt.Errorf("pageNumbers: %w", err)
		}
		if err := validateFloat(m, "startFrom", 0, 9999); err != nil {
			return fmt.Errorf("pageNumbers: %w", err)
		}
		if err := validateFloat(m, "margin", 0, maxMargin); err != nil {
			return fmt.Errorf("pageNumbers: %w", err)
		}
		if err := validateColorArray(m, "color"); err != nil {
			return fmt.Errorf("pageNumbers: %w", err)
		}
		if err := validatePosition(m, "position", []string{
			"bottom-center", "bottom-left", "bottom-right",
			"top-center", "top-left", "top-right",
		}); err != nil {
			return fmt.Errorf("pageNumbers: %w", err)
		}
	}

	// Validate redactions
	if redactions, ok := opts["redactions"]; ok {
		arr, ok := redactions.([]any)
		if !ok {
			return fmt.Errorf("redactions must be an array")
		}
		if len(arr) > maxRedactions {
			return fmt.Errorf("redactions exceeds maximum of %d", maxRedactions)
		}
		for i, r := range arr {
			m, ok := r.(map[string]any)
			if !ok {
				return fmt.Errorf("redaction[%d] must be an object", i)
			}
			if _, ok := toFloat64(m["page"]); !ok {
				return fmt.Errorf("redaction[%d] missing page", i)
			}
			rect, ok := m["rect"].([]any)
			if !ok || len(rect) != 4 {
				return fmt.Errorf("redaction[%d] rect must be [x, y, w, h]", i)
			}
			for j, v := range rect {
				n, ok := toFloat64(v)
				if !ok || n < 0 || n > 10000 {
					return fmt.Errorf("redaction[%d] rect[%d] out of range", i, j)
				}
			}
		}
	}

	return nil
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func validateFloat(m map[string]any, key string, min, max float64) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	n, ok := toFloat64(v)
	if !ok {
		return fmt.Errorf("%s must be a number", key)
	}
	if n < min || n > max {
		return fmt.Errorf("%s out of range [%.0f, %.0f]", key, min, max)
	}
	return nil
}

func validateColorArray(m map[string]any, key string) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 3 {
		return fmt.Errorf("%s must be [r, g, b]", key)
	}
	for _, c := range arr {
		n, ok := toFloat64(c)
		if !ok || n < 0 || n > 1 {
			return fmt.Errorf("%s values must be in [0, 1]", key)
		}
	}
	return nil
}

func validatePosition(m map[string]any, key string, allowed []string) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	for _, a := range allowed {
		if s == a {
			return nil
		}
	}
	return fmt.Errorf("%s invalid position: %q", key, s)
}
