package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
)

func TestOpenRegularFileNoFollowRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	file, _, err := openRegularFileNoFollow(link)
	if err == nil {
		file.Close()
		t.Fatal("expected symlink output to be rejected")
	}
}

func TestOpenRegularFileUnderDirRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(out, "leak.txt")); err != nil {
		t.Fatal(err)
	}

	file, _, err := openRegularFileUnderDir(out, "leak.txt")
	if err == nil {
		file.Close()
		t.Fatal("expected escaping symlink to be rejected")
	}
}

func TestOpenRegularFileUnderDirRejectsTraversalSegment(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "file.txt"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}

	file, _, err := openRegularFileUnderDir(out, "safe/../file.txt")
	if err == nil {
		file.Close()
		t.Fatal("expected ambiguous traversal segment to be rejected")
	}
	if !errors.Is(err, errInvalidOutputPath) {
		t.Fatalf("expected invalid path error, got %v", err)
	}
}

func TestOpenRegularFileUnderDirMissingFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}

	file, _, err := openRegularFileUnderDir(out, "missing.txt")
	if err == nil {
		file.Close()
		t.Fatal("expected missing output file")
	}
	if !errors.Is(err, errOutputFileMissing) {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestDownloadZipRejectsInvalidManifestBeforeMarkDownloaded(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}

	q := queue.New(1, nil)
	job, _, err := q.Submit("job-1", queue.ConversionDecompress, "", "archive.zip", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusDone
	job.OutputPath = out
	job.Metadata = map[string]any{
		"files": []map[string]any{
			{"name": "missing.txt", "size": int64(1), "index": 0},
		},
	}

	h := NewConvertHandler(&config.Config{TmpfsPath: dir}, q, stats.New("", "", ""))
	req := httptest.NewRequest(http.MethodGet, "/api/download/job-1/zip", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobId", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rec := httptest.NewRecorder()

	h.DownloadZip(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing manifest file to return 404, got %d", rec.Code)
	}
	if !job.MarkDownloaded() {
		t.Fatal("invalid zip download should not mark the job as downloaded")
	}
}

func TestDownloadRejectsMissingOutputBeforeMarkDownloaded(t *testing.T) {
	dir := t.TempDir()

	q := queue.New(1, nil)
	job, _, err := q.Submit("job-1", queue.ConversionImageFormat, "", "image.png", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusDone
	job.OutputPath = filepath.Join(dir, "missing.png")

	h := NewConvertHandler(&config.Config{TmpfsPath: dir}, q, stats.New("", "", ""))
	req := httptest.NewRequest(http.MethodGet, "/api/download/job-1", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobId", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected missing output to return 500, got %d", rec.Code)
	}
	if !job.MarkDownloaded() {
		t.Fatal("failed download should not mark the job as downloaded")
	}
}
