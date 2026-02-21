package api

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/perspectiveanalytics/filemagic-backend/internal/api/handlers"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
)

func NewRouter(ctx context.Context, cfg *config.Config, q *queue.Queue, statsStore *stats.Store) *chi.Mux {
	r := chi.NewRouter()

	rateLimiter := NewRateLimiter(ctx, cfg, cfg.RateLimitRPM, cfg.RateLimitRPH)
	pollLimiter := NewRateLimiter(ctx, cfg, 60, 600)
	turnstile := TurnstileMiddleware(cfg)

	r.Use(middleware.Recoverer)
	r.Use(SecurityHeaders)
	r.Use(CORSWithConfig(cfg))
	r.Use(RequestLogger)

	healthHandler := handlers.NewHealthHandler(q)
	queueHandler := handlers.NewQueueHandler(q)
	convertHandler := handlers.NewConvertHandler(cfg, q, statsStore)
	inspectHandler := handlers.NewInspectHandler(cfg)
	contactHandler := handlers.NewContactHandler(cfg)
	statsHandler := handlers.NewStatsHandler(statsStore)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", healthHandler.Health)
		r.With(pollLimiter.Middleware).Get("/stats", statsHandler.Get)

		r.With(rateLimiter.Middleware, turnstile).Post("/stats/thanks", statsHandler.Thanks)

		r.Route("/queue", func(r chi.Router) {
			r.Use(pollLimiter.Middleware)

			r.Get("/status", queueHandler.Status)
			r.Get("/position/{jobId}", queueHandler.Position)
		})

		r.Route("/convert", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/image", convertHandler.ImageConvert)
			r.Post("/pdf/compress", convertHandler.PDFCompress)
			r.Post("/image/compress", convertHandler.ImageCompress)
			r.Post("/ocr", convertHandler.OCR)
			r.Post("/metadata/remove", convertHandler.MetadataRemove)
			r.Post("/metadata/inspect", convertHandler.MetadataInspect)
			r.Post("/pdf/password", convertHandler.PDFPassword)
			r.Post("/pdf/edit", convertHandler.PDFEdit)
			r.Post("/pdf/extract-images", convertHandler.PDFExtractImages)
			r.Post("/certificate", convertHandler.CertConvert)
			r.Post("/markdown/pdf", convertHandler.MarkdownPDF)
			r.Post("/audio/extract", convertHandler.AudioExtract)
			r.Post("/audio", convertHandler.AudioConvert)
			r.Post("/video/compress", convertHandler.VideoCompress)
			r.Post("/video/mov-to-mp4", convertHandler.MovToMp4)
			r.Post("/video/gif", convertHandler.VideoToGif)
			r.Post("/svg/png", convertHandler.SvgToPng)
			r.Post("/favicon", convertHandler.Favicon)
			r.Post("/font", convertHandler.FontConvert)
			r.Post("/pdf/repair", convertHandler.PDFRepair)
			r.Post("/ebook", convertHandler.EbookConvert)
		})

		r.Route("/merge", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/pdf", convertHandler.PDFMerge)
			r.Post("/image-to-pdf", convertHandler.ImageToPDF)
		})

		r.Route("/generate", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/qrcode", convertHandler.QRCode)
		})

		r.Route("/inspect", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/certificate", inspectHandler.Certificate)
			r.Post("/certificate/text", inspectHandler.CertificateText)
		})

		r.Route("/archive", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/create", convertHandler.Archive)
			r.Post("/decompress", convertHandler.Decompress)
		})

		r.Route("/contact", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)
			r.Use(turnstile)

			r.Post("/email", contactHandler.Email)
		})

		r.Route("/download", func(r chi.Router) {
			r.Use(rateLimiter.Middleware)

			r.Get("/{jobId}", convertHandler.Download)
			r.Get("/{jobId}/file/{index}", convertHandler.DownloadFile)
			r.Get("/{jobId}/zip", convertHandler.DownloadZip)
		})
	})

	return r
}
