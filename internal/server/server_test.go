package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"movie-showtimes/internal/letterboxd"
	"movie-showtimes/internal/model"
	"movie-showtimes/internal/scraper"
	"movie-showtimes/internal/tmdb"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"web/templates/index.html": {Data: []byte("<html><body>hi</body></html>")},
		"web/static/app.js":        {Data: []byte(strings.Repeat("// filler comment line\n", 200))},
	}
}

// newTestServer runs each server in its own working directory so the
// data/*.json paths the cache and config packages use stay per-test.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := New(testFS(), tmdb.NewService(), letterboxd.New())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestShowtimesPayloadIsCachedAndRevalidates(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/showtimes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on payload")
	}
	first := srv.payload
	if first == nil {
		t.Fatal("payload was not cached")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/showtimes", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("got %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("304 carried a %d byte body", rec.Body.Len())
	}
	if srv.payload != first {
		t.Fatal("payload was rebuilt instead of served from cache")
	}

	srv.invalidatePayload()
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/showtimes", nil))
	if srv.payload == first {
		t.Fatal("invalidatePayload did not force a rebuild")
	}
	if got := rec.Header().Get("ETag"); got != etag {
		t.Fatalf("ETag changed without a data change: %q vs %q", got, etag)
	}
}

func TestStaticAssetsGzipAndRevalidate(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("compressible asset was served uncompressed")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != len(testFS()["web/static/app.js"].Data) {
		t.Fatalf("decompressed %d bytes, want %d", len(body), len(testFS()["web/static/app.js"].Data))
	}

	etag := rec.Header().Get("ETag")
	req = httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("got %d, want 304", rec.Code)
	}
}

func TestStaticAssetMissing(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/nope.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestRefreshRequiresAdminToken(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("ADMIN_TOKEN", "secret")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/refresh", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestBuildPayloadDropsShowtimesFromDeletedTheaters(t *testing.T) {
	today, _ := scraper.WindowDates()
	shows := &model.ShowtimeCache{
		RefreshedAt: time.Now(),
		Errors:      map[string]string{},
		Showtimes: []model.Showtime{
			{TheaterID: "keep", TheaterName: "Keep", Title: "Kept Film", Date: today, Time: "12:00"},
			{TheaterID: "gone", TheaterName: "Gone", Title: "Orphan Film", Date: today, Time: "13:00"},
		},
	}
	theaters := []model.Theater{{ID: "keep", Name: "Keep", Parser: "keep"}}

	payload := buildPayload(shows, theaters, map[string]model.FilmTMDB{}, tmdb.NewService(), letterboxd.New())

	for _, s := range payload.Showtimes {
		if s.TheaterID == "gone" {
			t.Errorf("payload still carries showtime from deleted theater: %+v", s)
		}
	}
	for _, day := range payload.Grid {
		for _, film := range day.Films {
			for _, s := range film.Showtimes {
				if s.TheaterID == "gone" {
					t.Errorf("grid still carries showtime from deleted theater: %+v", s)
				}
			}
		}
	}
}

func TestRefreshParisLive(t *testing.T) {
	if os.Getenv("PARIS_LIVE") != "1" {
		t.Skip("set PARIS_LIVE=1")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..")
	t.Setenv("ADMIN_TOKEN", "secret")
	srv := newTestServer(t)
	theatersSrc := filepath.Join(root, "data", "theaters.json")
	theatersDst := filepath.Join("data", "theaters.json")
	if err := copyFile(theatersSrc, theatersDst); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	req.Header.Set("X-Admin-Token", "secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status %d: %s", rec.Code, rec.Body.String())
	}
	var payload model.APIPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, st := range payload.TheaterStatus {
		if st.Theater.ID != "paris" {
			continue
		}
		if st.Status != "ok" {
			t.Fatalf("paris status %q: %s", st.Status, st.Error)
		}
	}
	n := 0
	for _, s := range payload.Showtimes {
		if s.TheaterID == "paris" {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no paris showtimes in refresh payload")
	}
	t.Logf("paris showtimes: %d", n)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func TestBuildGridGroupsAndSorts(t *testing.T) {
	today, _ := scraper.WindowDates()
	shows := []model.Showtime{
		{TheaterID: "a", TheaterName: "A", Title: "Stalker", Date: today, Time: "9:00pm"},
		{TheaterID: "b", TheaterName: "B", Title: "Stalker", Date: today, Time: "2:00pm", Director: "Tarkovsky"},
		{TheaterID: "a", TheaterName: "A", Title: "Solaris", Date: today, Time: "11:00am"},
		{TheaterID: "a", TheaterName: "A", Title: "Out Of Window", Date: "1999-01-01", Time: "1:00pm"},
	}

	grid := buildGrid(shows, map[string]model.FilmTMDB{}, letterboxd.New())
	if len(grid) != 14 {
		t.Fatalf("grid has %d days, want 14", len(grid))
	}
	day := grid[0]
	if day.Date != today {
		t.Fatalf("first day is %s, want %s", day.Date, today)
	}
	if len(day.Films) != 2 {
		t.Fatalf("got %d films, want 2 (out-of-window showtime should be dropped)", len(day.Films))
	}
	if day.Films[0].Title != "Solaris" {
		t.Fatalf("films not sorted by first showtime: %v", day.Films[0].Title)
	}
	stalker := day.Films[1]
	if len(stalker.Showtimes) != 2 {
		t.Fatalf("Stalker has %d showtimes, want 2", len(stalker.Showtimes))
	}
	if stalker.Showtimes[0].Time != "14:00" || stalker.Showtimes[1].Time != "21:00" {
		t.Fatalf("showtimes not normalized/sorted: %+v", stalker.Showtimes)
	}
	if stalker.Director != "Tarkovsky" {
		t.Fatalf("director not merged from the later showtime: %q", stalker.Director)
	}
}
