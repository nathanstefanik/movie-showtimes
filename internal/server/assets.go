package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// asset is one embedded static file, pre-hashed and pre-compressed at startup.
// http.FileServer over an embed.FS emits neither ETag nor Last-Modified (the
// embedded files have a zero modtime), so every reload re-downloaded the full
// CSS and JS.
type asset struct {
	body        []byte
	gzip        []byte
	etag        string
	contentType string
}

type assetHandler struct {
	assets map[string]*asset
}

func newAssetHandler(staticFS fs.FS) (*assetHandler, error) {
	h := &assetHandler{assets: map[string]*asset{}}
	err := fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(staticFS, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(body)
		}
		a := &asset{
			body:        body,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			contentType: ct,
		}
		if gz, ok := gzipBytes(body); ok {
			a.gzip = gz
		}
		h.assets[p] = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (h *assetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a, ok := h.assets[strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	head := w.Header()
	head.Set("Content-Type", a.contentType)
	head.Set("ETag", a.etag)
	// no-cache means "revalidate", not "don't store": the browser still holds
	// the bytes and a reload costs a 304 instead of the whole file, while a
	// deploy takes effect immediately.
	head.Set("Cache-Control", "no-cache")
	head.Set("Vary", "Accept-Encoding")
	if etagMatch(r, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.body
	if a.gzip != nil && acceptsGzip(r) {
		head.Set("Content-Encoding", "gzip")
		body = a.gzip
	}
	head.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

func etagMatch(r *http.Request, etag string) bool {
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
