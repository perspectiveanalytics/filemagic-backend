package converter

import (
	"context"
	"fmt"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type Converter interface {
	Convert(ctx context.Context, job *queue.Job) error
	Type() queue.ConversionType
}

type Registry struct {
	converters map[queue.ConversionType]Converter
	cfg        *config.Config
	runner     *NsjailRunner
}

func NewRegistry(cfg *config.Config) (*Registry, error) {
	runner := NewNsjailRunner(cfg)

	if err := runner.ValidateConfigs(); err != nil {
		return nil, err
	}

	r := &Registry{
		converters: make(map[queue.ConversionType]Converter),
		cfg:        cfg,
		runner:     runner,
	}

	r.Register(NewImageConverter(cfg, runner))
	r.Register(NewPDFCompressor(cfg, runner))
	r.Register(NewImageCompressor(cfg, runner))
	r.Register(NewOCRConverter(cfg, runner))
	r.Register(NewMetadataRemover(cfg, runner))
	r.Register(NewPDFMerger(cfg, runner))
	r.Register(NewImageToPDFConverter(cfg, runner))
	r.Register(NewQRCodeGenerator(cfg))
	r.Register(NewCertConverter(cfg, runner))
	r.Register(NewArchiver(cfg, runner))
	r.Register(NewMetadataInspector(cfg, runner))
	r.Register(NewMarkdownPDFConverter(cfg, runner))
	r.Register(NewAudioExtractor(cfg, runner))
	r.Register(NewAudioConverter(cfg, runner))
	r.Register(NewVideoCompressor(cfg, runner))
	r.Register(NewMovToMp4Converter(cfg, runner))
	r.Register(NewVideoToGifConverter(cfg, runner))
	r.Register(NewDecompressor(cfg, runner))
	r.Register(NewPDFPasswordConverter(cfg, runner))
	r.Register(NewSvgToPngConverter(cfg, runner))
	r.Register(NewPDFImageExtractor(cfg, runner))
	r.Register(NewPDFEditor(cfg, runner))
	r.Register(NewFaviconGenerator(cfg, runner))
	r.Register(NewFontConverter(cfg, runner))
	r.Register(NewPDFRepairer(cfg, runner))
	r.Register(NewEbookConverter(cfg, runner))

	return r, nil
}

func (r *Registry) Register(c Converter) {
	r.converters[c.Type()] = c
}

func (r *Registry) Get(t queue.ConversionType) (Converter, error) {
	c, ok := r.converters[t]
	if !ok {
		return nil, fmt.Errorf("no converter registered for type: %s", t)
	}
	return c, nil
}

func (r *Registry) Process(ctx context.Context, job *queue.Job) error {
	c, err := r.Get(job.ConversionType)
	if err != nil {
		return err
	}
	return c.Convert(ctx, job)
}
