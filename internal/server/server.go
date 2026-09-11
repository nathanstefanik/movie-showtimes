package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"movie-showtimes/internal/cache"
	"movie-showtimes/internal/config"
	"movie-showtimes/internal/letterboxd"
	"movie-showtimes/internal/model"
	"movie-showtimes/internal/scraper"
	"movie-showtimes/internal/tmdb"
)

const maxJSONBody = 1 << 20 // 1 MiB

type Server struct {
	tmdb       *tmdb.Service
	letterboxd *letterboxd.Library
	mux        *http.ServeMux
	tmpl       *template.Template

	// mu guards shows, which /api/refresh replaces while readers are mid-flight.
	mu    sync.RWMutex
	shows *model.ShowtimeCache

	// payloadMu guards the rendered /api/showtimes response. Building it walks
	// every showtime, reads theaters.json and consults the Letterboxd library,
	// and the result only changes on refresh or at the day boundary — so render
	// it once and hand out bytes.
	payloadMu sync.Mutex
	payload   *renderedPayload

	indexMu   sync.Mutex
	indexHTML []byte
	indexGzip []byte
	indexETag string
}

type renderedPayload struct {
	json    []byte
	gzip    []byte
	etag    string
	day     string
	builtAt time.Time
}

// payloadTTL bounds how long a cached payload is reused, so edits to the
// Letterboxd CSVs or a cache file rewritten out of band still show up without
// a restart.
const payloadTTL = 30 * time.Second

func New(webFS fs.FS, tmdbSvc *tmdb.Service, lb *letterboxd.Library) (*Server, error) {
	shows, err := cache.LoadShowtimes()
	if err != nil {
		return nil, err
	}
	tmpl, err := template.ParseFS(webFS, "web/templates/*.html")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		return nil, err
	}
	assets, err := newAssetHandler(staticFS)
	if err != nil {
		return nil, err
	}

	s := &Server{
		tmdb:       tmdbSvc,
		letterboxd: lb,
		shows:      shows,
		mux:        http.NewServeMux(),
		tmpl:       tmpl,
	}
	s.mux.Handle("/", http.HandlerFunc(s.handleIndex))
	s.mux.Handle("/static/", http.StripPrefix("/static/", assets))
	s.mux.HandleFunc("/api/showtimes", s.handleShowtimes)
	s.mux.HandleFunc("/api/refresh", s.handleRefresh)
	s.mux.HandleFunc("/api/theaters", s.handleTheaters)
	s.mux.HandleFunc("/api/theaters/", s.handleTheaterByID)
	if tmdbSvc.Configured() && shows != nil && len(shows.Showtimes) > 0 {
		showsCopy := shows.Showtimes
		go func() { _ = tmdbSvc.RefreshForShows(showsCopy) }()
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return securityHeaders(s.mux)
}

// securityHeaders is defense in depth for the admin token, which lives in
// sessionStorage and would be readable by any injected script. Every asset is
// same-origin except TMDB posters, and there is no inline script or style, so
// the policy can stay strict without 'unsafe-inline'.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' https://image.tmdb.org; " +
		"connect-src 'self'; " +
		"form-action 'none'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// requireAdmin gates mutating endpoints behind ADMIN_TOKEN (X-Admin-Token or
// Authorization: Bearer). Fail closed when the token is unset.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	want := os.Getenv("ADMIN_TOKEN")
	if want == "" {
		http.Error(w, "admin token not configured", http.StatusServiceUnavailable)
		return false
	}
	got := r.Header.Get("X-Admin-Token")
	if got == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			got = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if !tokenEqual(got, want) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func tokenEqual(got, want string) bool {
	if len(got) != len(want) {
		subtle.ConstantTimeCompare([]byte(want), []byte(want))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" || u.User != nil {
		return false
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
	if err := dec.Decode(dst); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return false
	}
	return true
}

// handleIndex renders the (static) template once and then serves the cached
// bytes; re-executing the template per request bought nothing.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.indexMu.Lock()
	if s.indexHTML == nil {
		var buf bytes.Buffer
		if err := s.tmpl.ExecuteTemplate(&buf, "index.html", map[string]any{}); err != nil {
			s.indexMu.Unlock()
			http.Error(w, "template error", http.StatusInternalServerError)
			return
		}
		s.indexHTML = buf.Bytes()
		sum := sha256.Sum256(s.indexHTML)
		s.indexETag = `"` + hex.EncodeToString(sum[:16]) + `"`
		if gz, ok := gzipBytes(s.indexHTML); ok {
			s.indexGzip = gz
		}
	}
	html, gz, etag := s.indexHTML, s.indexGzip, s.indexETag
	s.indexMu.Unlock()

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Vary", "Accept-Encoding")
	if etagMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeMaybeGzip(w, r, "text/html; charset=utf-8", html, gz)
}

func (s *Server) handleShowtimes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writePayload(w, r)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	theaters, err := config.LoadTheaters()
	if err != nil {
		http.Error(w, "failed to load theaters", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	showtimes, errors := scraper.FetchAll(ctx, scraper.DefaultClient(), theaters)
	shows := &model.ShowtimeCache{
		RefreshedAt: time.Now(),
		Showtimes:   showtimes,
		Errors:      errors,
	}
	if err := cache.SaveShowtimes(shows); err != nil {
		http.Error(w, "failed to save showtimes", http.StatusInternalServerError)
		return
	}
	s.setShows(shows)
	if s.tmdb.Configured() {
		_ = s.tmdb.RefreshForShows(showtimes)
		s.invalidatePayload()
	}
	s.writePayload(w, r)
}

func (s *Server) setShows(shows *model.ShowtimeCache) {
	s.mu.Lock()
	s.shows = shows
	s.mu.Unlock()
	s.invalidatePayload()
}

func (s *Server) currentShows() *model.ShowtimeCache {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shows
}

func (s *Server) invalidatePayload() {
	s.payloadMu.Lock()
	s.payload = nil
	s.payloadMu.Unlock()
}

func (s *Server) handleTheaters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		theaters, err := config.LoadTheaters()
		if err != nil {
			http.Error(w, "failed to load theaters", http.StatusInternalServerError)
			return
		}
		writeJSON(w, theaters)
	case http.MethodPost:
		if !requireAdmin(w, r) {
			return
		}
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if !decodeJSONBody(w, r, &req) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.URL = strings.TrimSpace(req.URL)
		if req.Name == "" || req.URL == "" {
			http.Error(w, "name and url required", http.StatusBadRequest)
			return
		}
		if !validHTTPURL(req.URL) {
			http.Error(w, "url must be http or https", http.StatusBadRequest)
			return
		}
		theaters, err := config.LoadTheaters()
		if err != nil {
			http.Error(w, "failed to load theaters", http.StatusInternalServerError)
			return
		}
		th := model.Theater{
			ID:     config.Slugify(req.Name, theaters),
			Name:   req.Name,
			URL:    req.URL,
			Parser: "",
		}
		theaters = append(theaters, th)
		if err := config.SaveTheaters(theaters); err != nil {
			http.Error(w, "failed to save theaters", http.StatusInternalServerError)
			return
		}
		s.invalidatePayload()
		writeJSON(w, th)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTheaterByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/theaters/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	theaters, err := config.LoadTheaters()
	if err != nil {
		http.Error(w, "failed to load theaters", http.StatusInternalServerError)
		return
	}
	var kept []model.Theater
	found := false
	for _, t := range theaters {
		if t.ID == id {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := config.SaveTheaters(kept); err != nil {
		http.Error(w, "failed to save theaters", http.StatusInternalServerError)
		return
	}
	s.invalidatePayload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writePayload(w http.ResponseWriter, r *http.Request) {
	rendered, err := s.renderedPayload()
	if err != nil {
		http.Error(w, "failed to build payload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", rendered.etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Vary", "Accept-Encoding")
	// Only a safe method may be answered with 304; /api/refresh reuses this
	// writer for its POST response.
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && etagMatch(r, rendered.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeMaybeGzip(w, r, "application/json", rendered.json, rendered.gzip)
}

func (s *Server) renderedPayload() (*renderedPayload, error) {
	today := time.Now().In(scraper.NYC()).Format("2006-01-02")

	s.payloadMu.Lock()
	defer s.payloadMu.Unlock()
	if p := s.payload; p != nil && p.day == today && time.Since(p.builtAt) < payloadTTL {
		return p, nil
	}

	theaters, err := config.LoadTheaters()
	if err != nil {
		return nil, err
	}
	tmdbMap, err := s.tmdb.LookupAll()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(buildPayload(s.currentShows(), theaters, tmdbMap, s.tmdb, s.letterboxd))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p := &renderedPayload{
		json:    data,
		etag:    `"` + hex.EncodeToString(sum[:16]) + `"`,
		day:     today,
		builtAt: time.Now(),
	}
	if gz, ok := gzipBytes(data); ok {
		p.gzip = gz
	}
	s.payload = p
	return p, nil
}

func buildPayload(shows *model.ShowtimeCache, theaters []model.Theater, tmdbMap map[string]model.FilmTMDB, tmdbSvc *tmdb.Service, lb *letterboxd.Library) model.APIPayload {
	var refreshed *time.Time
	if shows != nil && !shows.RefreshedAt.IsZero() {
		t := shows.RefreshedAt
		refreshed = &t
	}
	errors := map[string]string{}
	showtimes := []model.Showtime{}
	if shows != nil {
		showtimes = shows.Showtimes
		if shows.Errors != nil {
			errors = shows.Errors
		}
	}

	status := make([]model.TheaterStatus, 0, len(theaters))
	known := make(map[string]struct{}, len(theaters))
	for _, th := range theaters {
		known[th.ID] = struct{}{}
		st := model.TheaterStatus{Theater: th}
		if th.Parser == "" {
			st.Status = "no_parser"
		} else if msg, ok := errors[th.ID]; ok && msg != "" {
			st.Status = "error"
			st.Error = msg
		} else {
			st.Status = "ok"
		}
		status = append(status, st)
	}

	showtimes = filterKnownTheaters(showtimes, known)
	grid := buildGrid(showtimes, tmdbMap, lb)
	return model.APIPayload{
		RefreshedAt:          refreshed,
		Showtimes:            showtimes,
		Errors:               errors,
		TheaterStatus:        status,
		Grid:                 grid,
		LetterboxdConfigured: lb.Configured(),
		TMDBConfigured:       tmdbSvc.Configured(),
	}
}

// filterKnownTheaters drops showtimes left behind by a theater deleted from
// theaters.json. The next scrape would drop them anyway, but until then they
// would render with no theater entry to hide or delete them from.
func filterKnownTheaters(showtimes []model.Showtime, known map[string]struct{}) []model.Showtime {
	out := make([]model.Showtime, 0, len(showtimes))
	for _, st := range showtimes {
		if _, ok := known[st.TheaterID]; ok {
			out = append(out, st)
		}
	}
	return out
}

func buildGrid(showtimes []model.Showtime, tmdbMap map[string]model.FilmTMDB, lb *letterboxd.Library) []model.DayGrid {
	dates := scraper.WeekGridDates()
	windowStart, windowEnd := scraper.WindowDates()
	byDay := make(map[string]map[string]*model.FilmDayEntry, len(dates))

	// Letterboxd status is per film, not per showtime, so memoize it for the
	// length of this build instead of re-reading the library for every row.
	lbStatus := map[string]string{}

	for _, st := range showtimes {
		if !scraper.InWindowRange(st.Date, windowStart, windowEnd) {
			continue
		}
		key := scraper.NormalizeTitle(st.Title)
		day := byDay[st.Date]
		if day == nil {
			day = map[string]*model.FilmDayEntry{}
			byDay[st.Date] = day
		}
		entry, ok := day[key]
		tmdbMeta := tmdbMap[key]
		if !ok {
			status, cached := lbStatus[key]
			if !cached {
				status = lb.Status(st.Title)
				lbStatus[key] = status
			}
			entry = &model.FilmDayEntry{
				Title:            st.Title,
				NormalizedTitle:  key,
				TMDBURL:          scraper.TMDBSearchURL(st.Title),
				PosterURL:        tmdbMeta.PosterURL,
				Overview:         firstNonEmpty(st.Overview, tmdbMeta.Overview),
				Director:         firstNonEmpty(st.Director, tmdbMeta.Director),
				ReleaseYear:      firstNonEmpty(st.Year, tmdbMeta.ReleaseYear),
				LetterboxdStatus: status,
			}
			day[key] = entry
		} else {
			if entry.Director == "" {
				entry.Director = firstNonEmpty(st.Director, tmdbMeta.Director)
			}
			if entry.ReleaseYear == "" {
				entry.ReleaseYear = firstNonEmpty(st.Year, tmdbMeta.ReleaseYear)
			}
			if entry.Overview == "" {
				entry.Overview = firstNonEmpty(st.Overview, tmdbMeta.Overview)
			}
			if entry.PosterURL == "" {
				entry.PosterURL = tmdbMeta.PosterURL
			}
		}
		entry.Showtimes = append(entry.Showtimes, model.ShowtimeChip{
			Time:        scraper.NormalizeTime(st.Time),
			TheaterID:   st.TheaterID,
			TheaterName: st.TheaterName,
			FilmURL:     st.FilmURL,
		})
	}

	grid := make([]model.DayGrid, 0, len(dates))
	for _, d := range dates {
		dateStr := d.Format("2006-01-02")
		films := byDay[dateStr]
		day := model.DayGrid{
			Date:  dateStr,
			Label: scraper.FormatDateLabel(d),
			Films: make([]model.FilmDayEntry, 0, len(films)),
		}
		for _, film := range films {
			sort.Slice(film.Showtimes, func(i, j int) bool {
				return scraper.CompareShowtimeTime(film.Showtimes[i].Time, film.Showtimes[j].Time) < 0
			})
			day.Films = append(day.Films, *film)
		}
		sort.Slice(day.Films, func(i, j int) bool {
			fi, fj := day.Films[i].Showtimes, day.Films[j].Showtimes
			switch {
			case len(fi) == 0 && len(fj) == 0:
				return day.Films[i].Title < day.Films[j].Title
			case len(fi) == 0:
				return false
			case len(fj) == 0:
				return true
			default:
				if c := scraper.CompareShowtimeTime(fi[0].Time, fj[0].Time); c != 0 {
					return c < 0
				}
				return day.Films[i].Title < day.Films[j].Title
			}
		})
		grid = append(grid, day)
	}
	return grid
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
