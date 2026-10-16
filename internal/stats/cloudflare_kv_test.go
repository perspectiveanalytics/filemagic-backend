package stats

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestFlushRetainsCountsOnReadFailure(t *testing.T) {
	writes := 0
	s := &Store{enabled: true, client: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			writes++
		}
		return reply(500, "failure"), nil
	})}}
	s.pendingProcessed.Store(3)
	s.flush()
	if writes != 0 || s.pendingProcessed.Load() != 3 {
		t.Fatalf("writes=%d pending=%d", writes, s.pendingProcessed.Load())
	}
}

func TestFlushRetriesSameValueAndRetainsNewIncrements(t *testing.T) {
	var writes []string
	reads := 0
	s := &Store{enabled: true, client: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			reads++
			return reply(200, "100"), nil
		}
		b, _ := io.ReadAll(r.Body)
		writes = append(writes, string(b))
		if len(writes) == 1 {
			return reply(500, "failure"), nil
		}
		return reply(200, "{}"), nil
	})}}
	s.pendingProcessed.Store(3)
	s.flush()
	s.pendingProcessed.Add(2)
	s.flush()
	s.flush()
	if strings.Join(writes, ",") != "103,103,105" || reads != 2 {
		t.Fatalf("writes=%v reads=%d", writes, reads)
	}
	if s.pendingProcessed.Load() != 0 {
		t.Fatal("pending counter not flushed")
	}
}
