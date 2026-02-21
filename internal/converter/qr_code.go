package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	qrcode "github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"github.com/yeqown/go-qrcode/writer/standard/shapes"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type QRCodeGenerator struct {
	cfg *config.Config
}

func NewQRCodeGenerator(cfg *config.Config) *QRCodeGenerator {
	return &QRCodeGenerator{cfg: cfg}
}

func (g *QRCodeGenerator) Type() queue.ConversionType {
	return queue.ConversionQRCode
}

func (g *QRCodeGenerator) Convert(_ context.Context, job *queue.Job) error {
	text, ok := job.Options["text"].(string)
	if !ok || text == "" {
		return fmt.Errorf("missing or empty text")
	}

	if len(text) > 4296 {
		return fmt.Errorf("text too long (max 4296 characters for QR codes)")
	}

	size := 512
	if s, ok := job.Options["size"].(float64); ok && s >= 128 && s <= 2048 {
		size = int(s)
	}

	ecLevel := qrcode.ErrorCorrectionMedium
	if ec, ok := job.Options["errorCorrection"].(string); ok {
		switch strings.ToUpper(ec) {
		case "L":
			ecLevel = qrcode.ErrorCorrectionLow
		case "M":
			ecLevel = qrcode.ErrorCorrectionMedium
		case "Q":
			ecLevel = qrcode.ErrorCorrectionQuart
		case "H":
			ecLevel = qrcode.ErrorCorrectionHighest
		}
	}

	qrc, err := qrcode.NewWith(text,
		qrcode.WithErrorCorrectionLevel(ecLevel),
	)
	if err != nil {
		return fmt.Errorf("failed to generate QR code: %w", err)
	}

	// Compute block width to approximate the requested pixel size.
	// Final image = dimension * blockWidth + 2 * border (1px each side).
	const border = 1
	dim := qrc.Dimension()
	blockWidth := (size - 2*border) / dim
	if blockWidth < 2 {
		blockWidth = 2
	}
	if blockWidth > 255 {
		blockWidth = 255
	}

	_, _, outputDir, err := PrepareJobDir(g.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}
	outputFile := filepath.Join(outputDir, "qrcode.png")

	writerOpts := []standard.ImageOption{
		standard.WithBuiltinImageEncoder(standard.PNG_FORMAT),
		standard.WithQRWidth(uint8(blockWidth)),
		standard.WithBorderWidth(border),
	}

	if fg, ok := job.Options["fgColor"].(string); ok && isValidHexColor(fg) {
		writerOpts = append(writerOpts, standard.WithFgColorRGBHex(fg))
	}
	if bg, ok := job.Options["bgColor"].(string); ok && isValidHexColor(bg) {
		writerOpts = append(writerOpts, standard.WithBgColorRGBHex(bg))
	}

	if shape := buildQRShape(job.Options); shape != nil {
		writerOpts = append(writerOpts, standard.WithCustomShape(shape))
	}

	w, err := standard.New(outputFile, writerOpts...)
	if err != nil {
		return fmt.Errorf("failed to create QR writer: %w", err)
	}

	if err := qrc.Save(w); err != nil {
		return fmt.Errorf("failed to write QR code: %w", err)
	}

	outputInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = outputInfo.Size()

	return nil
}

// buildQRShape constructs a custom IShape from dotShape and eyeShape options.
// Returns nil when both are default (square), letting the library use its built-in rectangle.
func buildQRShape(options map[string]any) standard.IShape {
	dotShape, _ := options["dotShape"].(string)
	eyeShape, _ := options["eyeShape"].(string)

	if (dotShape == "" || dotShape == "square") && (eyeShape == "" || eyeShape == "square") {
		return nil
	}

	var blockFn func(ctx *standard.DrawContext)
	switch dotShape {
	case "circle":
		blockFn = shapes.CircleBlocks(1.0)
	case "liquid":
		blockFn = shapes.LiquidBlock()
	default:
		blockFn = shapes.SquareBlocks(1.0)
	}

	var finderFn func(ctx *standard.DrawContext)
	switch eyeShape {
	case "rounded":
		finderFn = shapes.RoundedFinder()
	default:
		finderFn = shapes.SquareFinder()
	}

	return shapes.Assemble(finderFn, blockFn)
}

// isValidHexColor checks that s is a valid "#RRGGBB" hex color.
func isValidHexColor(s string) bool {
	return hexColorRe.MatchString(s)
}
