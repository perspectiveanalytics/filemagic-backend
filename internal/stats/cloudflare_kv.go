package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	kvBaseURL     = "https://api.cloudflare.com/client/v4"
	keyProcessed  = "files_processed"
	keyThanks     = "thanks"
	httpTimeout   = 5 * time.Second
	flushInterval = 30 * time.Second
)

// Store manages counters in Cloudflare KV.
// All methods are safe for concurrent use.
// If not configured (empty account/namespace/token), all operations are no-ops.
type Store struct {
	accountID   string
	namespaceID string
	apiToken    string
	client      *http.Client
	enabled     bool

	// Local cache to avoid hitting KV on every homepage load.
	mu       sync.RWMutex
	cached   Stats
	cachedAt time.Time
	cacheTTL time.Duration

	// Pending increments batched in memory, flushed periodically.
	pendingProcessed atomic.Int64
	pendingThanks    atomic.Int64
	flushDone        chan struct{}
}

type Stats struct {
	FilesProcessed int64 `json:"filesProcessed"`
	Thanks         int64 `json:"thanks"`
}

func New(accountID, namespaceID, apiToken string) *Store {
	enabled := accountID != "" && namespaceID != "" && apiToken != ""
	if enabled {
		slog.Info("stats store enabled (Cloudflare KV)")
	} else {
		slog.Info("stats store disabled (missing FILEMAGIC_CF_* env vars)")
	}
	s := &Store{
		accountID:   accountID,
		namespaceID: namespaceID,
		apiToken:    apiToken,
		client:      &http.Client{Timeout: httpTimeout},
		enabled:     enabled,
		cacheTTL:    30 * time.Second,
		flushDone:   make(chan struct{}),
	}
	if enabled {
		go s.flushLoop()
	}
	return s
}

func (s *Store) Enabled() bool {
	return s.enabled
}

// IncrementProcessed increments the files_processed counter.
// The actual KV write is batched and flushed periodically.
func (s *Store) IncrementProcessed() {
	if !s.enabled {
		return
	}
	s.pendingProcessed.Add(1)
}

// IncrementThanks increments the thanks counter.
// The actual KV write is batched and flushed periodically.
func (s *Store) IncrementThanks() {
	if !s.enabled {
		return
	}
	s.pendingThanks.Add(1)
}

// Close flushes pending counters and stops the background loop.
func (s *Store) Close() {
	if !s.enabled {
		return
	}
	close(s.flushDone)
	s.flush()
}

// Get returns cached stats, refreshing from KV if stale.
func (s *Store) Get() Stats {
	if !s.enabled {
		return Stats{}
	}

	s.mu.RLock()
	if time.Since(s.cachedAt) < s.cacheTTL {
		st := s.cached
		s.mu.RUnlock()
		return st
	}
	s.mu.RUnlock()

	// Refresh cache.
	processed := s.getKey(keyProcessed)
	thanks := s.getKey(keyThanks)

	st := Stats{
		FilesProcessed: processed,
		Thanks:         thanks,
	}

	s.mu.Lock()
	s.cached = st
	s.cachedAt = time.Now()
	s.mu.Unlock()

	return st
}

func (s *Store) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.flush()
		case <-s.flushDone:
			return
		}
	}
}

func (s *Store) flush() {
	// Atomically swap pending counts to zero.
	p := s.pendingProcessed.Swap(0)
	t := s.pendingThanks.Swap(0)

	if p > 0 {
		s.incrementBy(keyProcessed, p)
	}
	if t > 0 {
		s.incrementBy(keyThanks, t)
	}
}

func (s *Store) incrementBy(key string, delta int64) {
	current := s.getKey(key)
	s.putKey(key, current+delta)

	// Invalidate cache.
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Store) getKey(key string) int64 {
	url := fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		kvBaseURL, s.accountID, s.namespaceID, key)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Error("stats: failed to create request", "key", key, "error", err)
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+s.apiToken)

	resp, err := s.client.Do(req) // #nosec G704 -- URL built from trusted server-side config, not user input
	if err != nil {
		slog.Error("stats: KV get failed", "key", key, "error", err)
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("stats: KV get unexpected status", "key", key, "status", resp.StatusCode)
		return 0
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		slog.Error("stats: failed to read KV response", "key", key, "error", err)
		return 0
	}

	var val int64
	if err := json.Unmarshal(body, &val); err != nil {
		slog.Error("stats: failed to parse KV value", "key", key, "raw", string(body), "error", err)
		return 0
	}
	return val
}

func (s *Store) putKey(key string, value int64) {
	url := fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		kvBaseURL, s.accountID, s.namespaceID, key)

	body, _ := json.Marshal(value)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("stats: failed to create put request", "key", key, "error", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req) // #nosec G704 -- URL built from trusted server-side config, not user input
	if err != nil {
		slog.Error("stats: KV put failed", "key", key, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("stats: KV put unexpected status", "key", key, "status", resp.StatusCode)
	}
}
