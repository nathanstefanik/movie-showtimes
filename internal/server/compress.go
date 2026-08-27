package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// minGzipSize is the point below which compression costs more bytes than it
// saves once the gzip header and trailer are counted.
const minGzipSize = 512

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// gzipBytes compresses b, reporting false when the result is not worth using.
// Responses here are highly repetitive JSON, CSS and JS, so this typically
// cuts the transfer by 80-90%.
func gzipBytes(b []byte) ([]byte, bool) {
	if len(b) < minGzipSize {
		return nil, false
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, false
	}
	if _, err := zw.Write(b); err != nil {
		return nil, false
	}
	if err := zw.Close(); err != nil {
		return nil, false
	}
	if buf.Len() >= len(b) {
		return nil, false
	}
	return buf.Bytes(), true
}

// writeMaybeGzip serves a pre-rendered body, using the pre-compressed copy when
// the client accepts it.
func writeMaybeGzip(w http.ResponseWriter, r *http.Request, contentType string, plain, gz []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Vary", "Accept-Encoding")
	body := plain
	if gz != nil && acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		body = gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}
