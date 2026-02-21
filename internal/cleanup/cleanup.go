package cleanup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type Cleaner struct {
	cfg   *config.Config
	queue *queue.Queue
}

func New(cfg *config.Config, q *queue.Queue) *Cleaner {
	return &Cleaner{cfg: cfg, queue: q}
}

func (c *Cleaner) Start(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.CleanupInterval)
	defer ticker.Stop()

	slog.Info("cleanup goroutine started",
		"interval", c.cfg.CleanupInterval,
		"maxAge", c.cfg.CleanupMaxAge,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("cleanup goroutine stopping")
			return
		case <-ticker.C:
			c.cleanupExpiredJobs()
			c.cleanupOrphanedDirs()
		}
	}
}

func (c *Cleaner) cleanupExpiredJobs() {
	now := time.Now()
	jobs := c.queue.GetAllJobs()

	for _, job := range jobs {
		if job.Status != queue.StatusDone && job.Status != queue.StatusError {
			continue
		}

		age := now.Sub(job.CompletedAt)
		if age < c.cfg.CleanupMaxAge {
			continue
		}

		jobDir := filepath.Join(c.cfg.TmpfsPath, job.ID)
		if err := os.RemoveAll(jobDir); err != nil {
			slog.Error("failed to remove expired job directory",
				"jobId", job.ID,
				"error", err,
			)
			continue
		}

		c.queue.DeleteJob(job.ID)
		slog.Info("cleaned up expired job",
			"jobId", job.ID,
			"age", age,
		)
	}
}

func (c *Cleaner) cleanupOrphanedDirs() {
	entries, err := os.ReadDir(c.cfg.TmpfsPath)
	if err != nil {
		if !os.IsNotExist(err) && !os.IsPermission(err) {
			slog.Error("failed to read tmpfs directory", "error", err)
		}
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(c.cfg.TmpfsPath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		age := now.Sub(info.ModTime())
		if age < c.cfg.CleanupMaxAge {
			continue
		}

		if _, found := c.queue.GetJob(entry.Name()); found {
			continue
		}

		if err := os.RemoveAll(dirPath); err != nil {
			slog.Error("failed to remove orphaned directory",
				"path", dirPath,
				"error", err,
			)
			continue
		}

		slog.Info("cleaned up orphaned directory",
			"path", dirPath,
			"age", age,
		)
	}
}
