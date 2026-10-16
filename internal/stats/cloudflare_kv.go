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
	flushMu          sync.Mutex
	writes           map[string]counterWrite
	confirmed        map[string]int64
}

type counterWrite struct {
	value int64
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
	processed, processedErr := s.getKey(keyProcessed)
	thanks, thanksErr := s.getKey(keyThanks)
	if processedErr != nil || thanksErr != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.cached
	}

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
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	if s.writes == nil {
		s.writes = make(map[string]counterWrite)
	}
	if s.confirmed == nil {
		s.confirmed = make(map[string]int64)
	}
	for key, pending := range map[string]*atomic.Int64{keyProcessed: &s.pendingProcessed, keyThanks: &s.pendingThanks} {
		write, retry := s.writes[key]
		if !retry {
			if pending.Load() == 0 {
				continue
			}
			current, err := s.getKey(key)
			if err != nil {
				continue
			}
			// KV reads may lag our own successful writes.
			current = max(current, s.confirmed[key])
			write = counterWrite{value: current + pending.Swap(0)}
			s.writes[key] = write
		}
		// Retry the same absolute value after an ambiguous PUT failure. Reading
		// and adding again could double-count a write accepted before a timeout.
		if err := s.putKey(key, write.value); err != nil {
			continue
		}
		s.confirmed[key] = write.value
		delete(s.writes, key)
		s.mu.Lock()
		s.cachedAt = time.Time{}
		s.mu.Unlock()
	}
}

func (s *Store) getKey(key string) (int64, error) {
	url := fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		kvBaseURL, s.accountID, s.namespaceID, key)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Error("stats: failed to create request", "key", key, "error", err)
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiToken)

	resp, err := s.client.Do(req) // #nosec G704 -- URL built from trusted server-side config, not user input
	if err != nil {
		slog.Error("stats: KV get failed", "key", key, "error", err)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("stats: KV get unexpected status", "key", key, "status", resp.StatusCode)
		return 0, fmt.Errorf("KV get status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		slog.Error("stats: failed to read KV response", "key", key, "error", err)
		return 0, err
	}

	var val int64
	if err := json.Unmarshal(body, &val); err != nil {
		slog.Error("stats: failed to parse KV value", "key", key, "error", err)
		return 0, err
	}
	return val, nil
}

func (s *Store) putKey(key string, value int64) error {
	url := fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		kvBaseURL, s.accountID, s.namespaceID, key)

	body, _ := json.Marshal(value)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("stats: failed to create put request", "key", key, "error", err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req) // #nosec G704 -- URL built from trusted server-side config, not user input
	if err != nil {
		slog.Error("stats: KV put failed", "key", key, "error", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("stats: KV put unexpected status", "key", key, "status", resp.StatusCode)
		return fmt.Errorf("KV put status %d", resp.StatusCode)
	}
	return nil
}
