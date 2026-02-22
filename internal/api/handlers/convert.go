package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"net"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/converter"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
	"github.com/perspectiveanalytics/filemagic-backend/internal/validator"
)

type ConvertHandler struct {
	cfg   *config.Config
	queue *queue.Queue
	stats *stats.Store
}

func NewConvertHandler(cfg *config.Config, q *queue.Queue, s *stats.Store) *ConvertHandler {
	return &ConvertHandler{cfg: cfg, queue: q, stats: s}
}

// isInternalRequest returns true when the request comes from a trusted IP
// (e.g. integration tests, monitoring). These should not count as user traffic.
func (h *ConvertHandler) isInternalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return h.cfg.TrustedIPs[host]
}

// Per-route file size limits (bytes). Each limit is the UI-facing value + 1 MB
// to avoid edge cases where a file passes frontend validation but is rejected here.
const (
	limitImageConvert  int64 = 21 << 20  // UI: 20 MB
	limitPDFCompress   int64 = 41 << 20  // UI: 40 MB
	limitImageCompress int64 = 21 << 20  // UI: 20 MB
	limitOCR           int64 = 6 << 20   // UI: 5 MB
	limitMetadata      int64 = 16 << 20  // UI: 15 MB
	limitCertConvert   int64 = 2 << 20   // UI: 1 MB
	limitMergePerFile    int64 = 9 << 20   // UI: 8 MB per file
	limitArchiveFile     int64 = 36 << 20  // UI: 35 MB per file
	limitMarkdownPDF     int64 = 6 << 20   // UI: 5 MB
	limitAudioExtract    int64 = 101 << 20 // UI: 100 MB
	limitAudioConvert    int64 = 51 << 20  // UI: 50 MB
	limitVideoCompress   int64 = 101 << 20 // UI: 100 MB
	limitMovToMp4        int64 = 101 << 20 // UI: 100 MB
	limitVideoToGif      int64 = 31 << 20  // UI: 30 MB
	limitPDFPassword     int64 = 31 << 20  // UI: 30 MB
	limitPDFEdit         int64 = 101 << 20 // UI: 100 MB
	limitSvgToPng        int64 = 21 << 20  // UI: 20 MB (ImageConvert page)
	limitPDFExtractImg   int64 = 31 << 20  // UI: 30 MB
	limitDecompress      int64 = 51 << 20  // UI: 50 MB
	limitFavicon         int64 = 21 << 20  // UI: 20 MB (ImageConvert page)
	limitFontConvert     int64 = 11 << 20  // UI: 10 MB
	limitPDFRepair       int64 = 21 << 20  // UI: 20 MB
	limitEbookConvert    int64 = 51 << 20  // UI: 50 MB

	maxMergeFileCount   = 10
	maxArchiveFileCount = 3
)

func (h *ConvertHandler) ImageConvert(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionImageFormat, validator.ValidateForImageConversion, limitImageConvert)
}

func (h *ConvertHandler) PDFCompress(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionPDFCompress, validator.ValidateForPDFCompression, limitPDFCompress)
}

func (h *ConvertHandler) ImageCompress(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionImageCompress, validator.ValidateForImageCompression, limitImageCompress)
}

func (h *ConvertHandler) OCR(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionOCR, validator.ValidateForOCR, limitOCR)
}

func (h *ConvertHandler) MetadataRemove(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionMetadataRemove, validator.ValidateForMetadataRemoval, limitMetadata)
}

func (h *ConvertHandler) MetadataInspect(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionMetadataInspect, validator.ValidateForMetadataRemoval, limitMetadata)
}

func (h *ConvertHandler) CertConvert(w http.ResponseWriter, r *http.Request) {
	h.handleCertConversion(w, r, queue.ConversionCertConvert)
}

func (h *ConvertHandler) Archive(w http.ResponseWriter, r *http.Request) {
	h.handleArchiveConversion(w, r, queue.ConversionArchive)
}

func (h *ConvertHandler) PDFMerge(w http.ResponseWriter, r *http.Request) {
	h.handleMergeConversion(w, r, queue.ConversionPDFMerge, validator.ValidateForPDFMerge, limitMergePerFile, maxMergeFileCount)
}

func (h *ConvertHandler) ImageToPDF(w http.ResponseWriter, r *http.Request) {
	h.handleMergeConversion(w, r, queue.ConversionImageToPDF, validator.ValidateForImageToPDF, limitMergePerFile, maxMergeFileCount)
}

func (h *ConvertHandler) AudioExtract(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionAudioExtract, validator.ValidateForAudioExtract, limitAudioExtract)
}

func (h *ConvertHandler) AudioConvert(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionAudioConvert, validator.ValidateForAudioConvert, limitAudioConvert)
}

func (h *ConvertHandler) VideoCompress(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionVideoCompress, validator.ValidateForVideoCompress, limitVideoCompress)
}

func (h *ConvertHandler) MovToMp4(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionMovToMp4, validator.ValidateForMovToMp4, limitMovToMp4)
}

func (h *ConvertHandler) VideoToGif(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionVideoToGif, validator.ValidateForVideoCompress, limitVideoToGif)
}

func (h *ConvertHandler) PDFPassword(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionPDFPassword, validator.ValidateForPDFPassword, limitPDFPassword)
}

func (h *ConvertHandler) PDFEdit(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionPDFEdit, validator.ValidateForPDFPassword, limitPDFEdit)
}

func (h *ConvertHandler) PDFExtractImages(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionPDFExtractImages, validator.ValidateForPDFExtractImages, limitPDFExtractImg)
}

func (h *ConvertHandler) Favicon(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionFavicon, validator.ValidateForImageConversion, limitFavicon)
}

func (h *ConvertHandler) PDFRepair(w http.ResponseWriter, r *http.Request) {
	h.handleConversion(w, r, queue.ConversionPDFRepair, validator.ValidateForPDFPassword, limitPDFRepair)
}

func (h *ConvertHandler) FontConvert(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(limitFontConvert); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitFontConvert {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file exceeds maximum size of 10 MB")
		return
	}

	if !validator.DetectFontFile(header.Filename) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "unsupported font format — accepted: .ttf, .otf, .woff, .woff2")
		return
	}

	var options map[string]any
	if optStr := r.FormValue("options"); optStr != "" {
		if err := json.Unmarshal([]byte(optStr), &options); err != nil {
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid options JSON")
			return
		}
	} else {
		options = make(map[string]any)
	}

	targetFormat, _ := options["targetFormat"].(string)
	validFormats := map[string]bool{"ttf": true, "otf": true, "woff": true, "woff2": true}
	if !validFormats[targetFormat] {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid target format")
		return
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	inputPath := filepath.Join(inputDir, converter.SanitizeFilename(header.Filename))
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, queue.ConversionFontConvert, inputPath, header.Filename, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("font conversion submitted", "jobId", job.ID, "position", position)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) EbookConvert(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(limitEbookConvert); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitEbookConvert {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file exceeds maximum size of 50 MB")
		return
	}

	if !validator.DetectEbookFile(header.Filename) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "unsupported ebook format")
		return
	}

	var options map[string]any
	if optStr := r.FormValue("options"); optStr != "" {
		if err := json.Unmarshal([]byte(optStr), &options); err != nil {
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid options JSON")
			return
		}
	} else {
		options = make(map[string]any)
	}

	targetFormat, _ := options["targetFormat"].(string)
	validFormats := map[string]bool{
		"epub": true, "mobi": true, "azw3": true,
		"txt": true, "fb2": true, "docx": true, "htmlz": true,
	}
	if !validFormats[targetFormat] {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid target format")
		return
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	inputPath := filepath.Join(inputDir, converter.SanitizeFilename(header.Filename))
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, queue.ConversionEbookConvert, inputPath, header.Filename, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("ebook conversion submitted", "jobId", job.ID, "position", position)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) SvgToPng(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(limitSvgToPng); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitSvgToPng {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file exceeds maximum size of 5 MB")
		return
	}

	if !validator.DetectSvgFile(header.Filename) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "only .svg files are supported")
		return
	}

	var options map[string]any
	if optStr := r.FormValue("options"); optStr != "" {
		if err := json.Unmarshal([]byte(optStr), &options); err != nil {
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid options JSON")
			return
		}
	} else {
		options = make(map[string]any)
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	inputPath := filepath.Join(inputDir, "input.svg")
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, queue.ConversionSvgToPng, inputPath, header.Filename, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("svg-to-png job submitted", "jobId", job.ID, "position", position)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) Decompress(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(limitDecompress); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitDecompress {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file exceeds maximum size of 50 MB")
		return
	}

	if !validator.DetectArchiveFile(header.Filename) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "unsupported archive format")
		return
	}

	options := make(map[string]any)
	if pw := r.FormValue("password"); pw != "" {
		options["password"] = pw
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	inputPath := filepath.Join(inputDir, converter.SanitizeFilename(header.Filename))
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, queue.ConversionDecompress, inputPath, header.Filename, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("decompress job submitted", "jobId", job.ID, "position", position)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) MarkdownPDF(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(limitMarkdownPDF); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitMarkdownPDF {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file exceeds maximum size of 5 MB")
		return
	}

	if !validator.DetectMarkdownFile(header.Filename) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "only .md and .markdown files are supported")
		return
	}

	// Reject binary files disguised as markdown (check for null bytes)
	probe := make([]byte, 512)
	n, _ := file.Read(probe)
	probe = probe[:n]
	file.Seek(0, 0)
	for _, b := range probe {
		if b == 0 {
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "file appears to be binary, not markdown")
			return
		}
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	inputPath := filepath.Join(inputDir, "input.md")
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, queue.ConversionMarkdownPDF, inputPath, header.Filename, nil, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("markdown-pdf job submitted",
		"jobId", job.ID,
		"position", position,
		"fileSize", header.Size,
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) handleConversion(
	w http.ResponseWriter,
	r *http.Request,
	convType queue.ConversionType,
	validateFunc func(validator.FileType) bool,
	maxFileSize int64,
) {
	if err := r.ParseMultipartForm(maxFileSize); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > maxFileSize {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, fmt.Sprintf("file exceeds maximum size of %d MB", maxFileSize/(1024*1024)))
		return
	}

	headerBytes := make([]byte, 32)
	n, _ := file.Read(headerBytes)
	headerBytes = headerBytes[:n]
	file.Seek(0, 0)

	fileType, err := validator.DetectFileType(bytes.NewReader(headerBytes))
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "unsupported file type")
		return
	}

	if !validateFunc(fileType) {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("file type %s is not supported for this conversion", fileType))
		return
	}

	var options map[string]any
	if optStr := r.FormValue("options"); optStr != "" {
		if err := json.Unmarshal([]byte(optStr), &options); err != nil {
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid options JSON")
			return
		}
	} else {
		options = make(map[string]any)
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	originalName := header.Filename
	safeExt := validator.GetExtension(fileType)
	inputPath := filepath.Join(inputDir, "input"+safeExt)

	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, convType, inputPath, originalName, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("job submitted",
		"jobId", job.ID,
		"type", convType,
		"position", position,
		"fileSize", header.Size,
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) handleMergeConversion(
	w http.ResponseWriter,
	r *http.Request,
	convType queue.ConversionType,
	validateFunc func(validator.FileType) bool,
	maxPerFile int64,
	maxFiles int,
) {
	if err := r.ParseMultipartForm(maxPerFile * int64(maxFiles)); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "files too large or invalid form data")
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) < 1 {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "at least 1 file is required")
		return
	}
	if len(files) > maxFiles {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("maximum %d files allowed", maxFiles))
		return
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
		return
	}

	var totalSize int64
	var fileNames []any

	for i, header := range files {
		if header.Size > maxPerFile {
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, fmt.Sprintf("file %q exceeds maximum size of %d MB", header.Filename, maxPerFile/(1024*1024)))
			return
		}

		file, err := header.Open()
		if err != nil {
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "failed to read uploaded file")
			return
		}

		headerBytes := make([]byte, 32)
		n, _ := file.Read(headerBytes)
		headerBytes = headerBytes[:n]
		file.Seek(0, 0)

		fileType, err := validator.DetectFileType(bytes.NewReader(headerBytes))
		if err != nil {
			file.Close()
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("file %q: unsupported file type", header.Filename))
			return
		}

		if !validateFunc(fileType) {
			file.Close()
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("file %q: type %s is not supported for this operation", header.Filename, fileType))
			return
		}

		safeExt := validator.GetExtension(fileType)
		fileName := fmt.Sprintf("%03d_input%s", i, safeExt)
		inputPath := filepath.Join(inputDir, fileName)

		dst, err := os.Create(inputPath)
		if err != nil {
			file.Close()
			os.RemoveAll(jobDir)
			slog.Error("failed to create input file", "error", err)
			response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
			return
		}

		if _, err := io.Copy(dst, file); err != nil {
			dst.Close()
			file.Close()
			os.RemoveAll(jobDir)
			slog.Error("failed to write input file", "error", err)
			response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
			return
		}
		dst.Close()
		file.Close()

		totalSize += header.Size
		fileNames = append(fileNames, fileName)
	}

	options := map[string]any{
		"files": fileNames,
	}

	job, position, err := h.queue.Submit(jobID, convType, inputDir, "merged.pdf", options, totalSize)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("merge job submitted",
		"jobId", job.ID,
		"type", convType,
		"position", position,
		"fileCount", len(files),
		"totalSize", totalSize,
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) handleCertConversion(
	w http.ResponseWriter,
	r *http.Request,
	convType queue.ConversionType,
) {
	if err := r.ParseMultipartForm(limitCertConvert); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > limitCertConvert {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, fmt.Sprintf("file exceeds maximum size of %d MB", limitCertConvert/(1024*1024)))
		return
	}

	// Validate certificate file extension
	certType, valid := validator.DetectCertFileType(header.Filename)
	if !valid {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "unsupported certificate file format")
		return
	}

	targetFormat := r.FormValue("targetFormat")
	if targetFormat == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing target format")
		return
	}

	validFormats := map[string]bool{"pem": true, "der": true, "p12": true, "p7b": true}
	if !validFormats[targetFormat] {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid target format")
		return
	}

	options := map[string]any{
		"targetFormat": targetFormat,
	}
	if pw := r.FormValue("password"); pw != "" {
		options["password"] = pw
	}
	if opw := r.FormValue("outputPassword"); opw != "" {
		options["outputPassword"] = opw
	}

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	// Use the validated extension (not the raw user-supplied one)
	safeExt := validator.GetCertExtension(certType)
	inputPath := filepath.Join(inputDir, "input"+safeExt)

	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("failed to create input file", "error", err)
		os.RemoveAll(jobDir)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		os.RemoveAll(jobDir)
		slog.Error("failed to write input file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process file")
		return
	}
	dst.Close()

	job, position, err := h.queue.Submit(jobID, convType, inputPath, header.Filename, options, header.Size)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("cert conversion submitted",
		"jobId", job.ID,
		"type", convType,
		"position", position,
		"targetFormat", targetFormat,
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) handleArchiveConversion(
	w http.ResponseWriter,
	r *http.Request,
	convType queue.ConversionType,
) {
	if err := r.ParseMultipartForm(limitArchiveFile * int64(maxArchiveFileCount)); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "files too large or invalid form data")
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) < 1 {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "at least 1 file is required")
		return
	}
	if len(files) > maxArchiveFileCount {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("maximum %d files allowed", maxArchiveFileCount))
		return
	}

	format := r.FormValue("format")
	if format == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing archive format")
		return
	}
	validFormats := map[string]bool{"zip": true, "7z": true, "tar.gz": true, "tar.zst": true}
	if !validFormats[format] {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid archive format")
		return
	}

	password := r.FormValue("password")

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	inputDir := filepath.Join(jobDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
		return
	}

	var totalSize int64
	var fileNames []any

	for _, header := range files {
		if header.Size > limitArchiveFile {
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, fmt.Sprintf("file %q exceeds maximum size of %d MB", header.Filename, limitArchiveFile/(1024*1024)))
			return
		}

		file, err := header.Open()
		if err != nil {
			os.RemoveAll(jobDir)
			response.Error(w, http.StatusBadRequest, response.CodeValidationError, "failed to read uploaded file")
			return
		}

		// Preserve original filename (sanitized) for archives
		safeName := converter.SanitizeFilename(header.Filename)
		inputPath := filepath.Join(inputDir, safeName)

		// Handle duplicate filenames by adding suffix
		if _, err := os.Stat(inputPath); err == nil {
			ext := filepath.Ext(safeName)
			base := strings.TrimSuffix(safeName, ext)
			for i := 1; ; i++ {
				safeName = fmt.Sprintf("%s_%d%s", base, i, ext)
				inputPath = filepath.Join(inputDir, safeName)
				if _, err := os.Stat(inputPath); os.IsNotExist(err) {
					break
				}
			}
		}

		dst, err := os.Create(inputPath)
		if err != nil {
			file.Close()
			os.RemoveAll(jobDir)
			slog.Error("failed to create input file", "error", err)
			response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
			return
		}

		if _, err := io.Copy(dst, file); err != nil {
			dst.Close()
			file.Close()
			os.RemoveAll(jobDir)
			slog.Error("failed to write input file", "error", err)
			response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process files")
			return
		}
		dst.Close()
		file.Close()

		totalSize += header.Size
		fileNames = append(fileNames, safeName)
	}

	options := map[string]any{
		"files":  fileNames,
		"format": format,
	}
	if password != "" {
		options["password"] = password
	}

	outputName := "archive." + format
	if format == "tar.gz" || format == "tar.zst" {
		outputName = "archive." + format
	}

	job, position, err := h.queue.Submit(jobID, convType, inputDir, outputName, options, totalSize)
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue conversion")
		return
	}

	slog.Info("archive job submitted",
		"jobId", job.ID,
		"type", convType,
		"position", position,
		"format", format,
		"fileCount", len(files),
		"totalSize", totalSize,
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) QRCode(w http.ResponseWriter, r *http.Request) {
	h.handleTextConversion(w, r, queue.ConversionQRCode, "qrcode.png")
}

const maxTextLength = 4096

func (h *ConvertHandler) handleTextConversion(
	w http.ResponseWriter,
	r *http.Request,
	convType queue.ConversionType,
	outputName string,
) {
	var body struct {
		Text    string         `json:"text"`
		Options map[string]any `json:"options"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid JSON body")
		return
	}

	text := strings.TrimSpace(body.Text)
	if text == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "text is required")
		return
	}
	if len(text) > maxTextLength {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, fmt.Sprintf("text exceeds maximum length of %d characters", maxTextLength))
		return
	}

	options := body.Options
	if options == nil {
		options = make(map[string]any)
	}
	options["text"] = text

	jobID := uuid.New().String()
	jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		slog.Error("failed to create job directory", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to process request")
		return
	}

	job, position, err := h.queue.Submit(jobID, convType, "", outputName, options, int64(len(text)))
	if err != nil {
		os.RemoveAll(jobDir)
		if _, ok := err.(queue.QueueFullError); ok {
			response.Error(w, http.StatusServiceUnavailable, response.CodeQueueFull, "server is busy, please try again later")
			return
		}
		slog.Error("failed to submit job", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to queue request")
		return
	}

	slog.Info("text job submitted",
		"jobId", job.ID,
		"type", convType,
		"position", position,
		"textLength", len(text),
	)

	response.JSON(w, http.StatusAccepted, response.SubmitResponse{
		JobID:         job.ID,
		Position:      position,
		EstimatedWait: position * 5,
	})
}

func (h *ConvertHandler) Download(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing job ID")
		return
	}

	job, found := h.queue.GetJob(jobID)
	if !found {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "job not found or expired")
		return
	}

	if job.Status != queue.StatusDone {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "job not ready for download")
		return
	}

	// Atomic CAS prevents double-download race (concurrent requests)
	if !job.MarkDownloaded() {
		response.Error(w, http.StatusGone, response.CodeNotFound, "file already downloaded")
		return
	}

	file, err := os.Open(job.OutputPath)
	if err != nil {
		slog.Error("failed to open output file", "error", err, "jobId", jobID)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to retrieve file")
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		slog.Error("failed to stat output file", "error", err, "jobId", jobID)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to retrieve file")
		return
	}

	ext := strings.ToLower(filepath.Ext(job.OutputPath))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	downloadName := filepath.Base(job.OutputPath)
	if job.OriginalName != "" {
		baseName := strings.TrimSuffix(job.OriginalName, filepath.Ext(job.OriginalName))
		downloadName = baseName + ext
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": downloadName}))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	_, err = io.Copy(w, file)
	if err != nil {
		slog.Error("failed to stream file", "error", err, "jobId", jobID)
		return
	}

	slog.Info("file downloaded", "jobId", jobID, "size", stat.Size())

	if !h.isInternalRequest(r) {
		h.stats.IncrementProcessed()
	}

	go func() {
		jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
		if err := os.RemoveAll(jobDir); err != nil {
			slog.Error("failed to cleanup job directory", "error", err, "jobId", jobID)
		}
		h.queue.DeleteJob(jobID)
		slog.Info("job cleaned up after download", "jobId", jobID)
	}()
}

// DownloadFile serves individual files from multi-file outputs.
// No MarkDownloaded() here — users need to download multiple files from the
// same job. Use DownloadZip for single-shot download of the complete set.
// Protected by: UUIDv4 job IDs (not guessable) + job expiry + rate limiting.
func (h *ConvertHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	indexStr := chi.URLParam(r, "index")
	if jobID == "" || indexStr == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing job ID or file index")
		return
	}

	idx, err := strconv.Atoi(indexStr)
	if err != nil || idx < 0 {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid file index")
		return
	}

	job, found := h.queue.GetJob(jobID)
	if !found {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "job not found or expired")
		return
	}

	if job.Status != queue.StatusDone {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "job not ready for download")
		return
	}

	filesRaw, ok := job.Metadata["files"].([]map[string]any)
	if !ok {
		// Try []any (JSON round-trip may produce this)
		if filesAny, ok2 := job.Metadata["files"].([]any); ok2 {
			filesRaw = make([]map[string]any, len(filesAny))
			for i, f := range filesAny {
				if m, ok3 := f.(map[string]any); ok3 {
					filesRaw[i] = m
				}
			}
		}
	}

	if idx >= len(filesRaw) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "file index out of range")
		return
	}

	entry := filesRaw[idx]
	name, _ := entry["name"].(string)
	if name == "" {
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "invalid file manifest entry")
		return
	}

	filePath := filepath.Join(job.OutputPath, name)

	// Security: ensure resolved path stays within outputDir
	absPath, _ := filepath.Abs(filePath)
	absOutput, _ := filepath.Abs(job.OutputPath)
	if !strings.HasPrefix(absPath, absOutput+string(filepath.Separator)) && absPath != absOutput {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid file path")
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "file not found")
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to read file")
		return
	}

	ext := strings.ToLower(filepath.Ext(name))
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	io.Copy(w, file)
}

func (h *ConvertHandler) DownloadZip(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing job ID")
		return
	}

	job, found := h.queue.GetJob(jobID)
	if !found {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "job not found or expired")
		return
	}

	if job.Status != queue.StatusDone {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "job not ready for download")
		return
	}

	if !job.MarkDownloaded() {
		response.Error(w, http.StatusGone, response.CodeNotFound, "file already downloaded")
		return
	}

	filesRaw, ok := job.Metadata["files"].([]map[string]any)
	if !ok {
		if filesAny, ok2 := job.Metadata["files"].([]any); ok2 {
			filesRaw = make([]map[string]any, len(filesAny))
			for i, f := range filesAny {
				if m, ok3 := f.(map[string]any); ok3 {
					filesRaw[i] = m
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "files.zip"}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, entry := range filesRaw {
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}

		filePath := filepath.Join(job.OutputPath, name)

		absPath, _ := filepath.Abs(filePath)
		absOutput, _ := filepath.Abs(job.OutputPath)
		if !strings.HasPrefix(absPath, absOutput+string(filepath.Separator)) {
			continue
		}

		file, err := os.Open(filePath)
		if err != nil {
			continue
		}

		zw, err := zipWriter.Create(name)
		if err != nil {
			file.Close()
			continue
		}

		io.Copy(zw, file)
		file.Close()
	}

	if !h.isInternalRequest(r) {
		h.stats.IncrementProcessed()
	}

	go func() {
		jobDir := filepath.Join(h.cfg.TmpfsPath, jobID)
		if err := os.RemoveAll(jobDir); err != nil {
			slog.Error("failed to cleanup job directory", "error", err, "jobId", jobID)
		}
		h.queue.DeleteJob(jobID)
		slog.Info("job cleaned up after zip download", "jobId", jobID)
	}()
}
