package handlers

import "net/http"

const (
	maxMultipartFieldBytes int64 = 1 << 20 // 1 MB for options, passwords and MIME overhead
	maxInt64                     = int64(1<<63 - 1)
)

func parseMultipartFormLimited(w http.ResponseWriter, r *http.Request, maxPayloadBytes int64) error {
	bodyLimit := maxPayloadBytes + maxMultipartFieldBytes
	if bodyLimit < maxPayloadBytes {
		bodyLimit = maxInt64
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	return r.ParseMultipartForm(maxPayloadBytes)
}
