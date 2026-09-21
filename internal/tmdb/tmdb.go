package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"movie-showtimes/internal/cache"
	"movie-showtimes/internal/model"
	"movie-showtimes/internal/scraper"
)

const (
	posterBase       = "https://image.tmdb.org/t/p/w185"
	maxSearchResults = 5
)

type Service struct {
	APIKey string
	mu     sync.Mutex // serializes RefreshForShows (cache read/write)

	// lookupMu guards the memoized view of tmdb.json. Without it every
	// /api/showtimes request re-read and re-parsed the whole cache file.
	lookupMu   sync.Mutex
	lookup     map[string]model.FilmTMDB
	lookupStat cache.FileStamp
}

func NewService() *Service {
	return &Service{APIKey: os.Getenv("TMDB_API_KEY")}
}

func (s *Service) Configured() bool {
	return s.APIKey != ""
}

func (s *Service) RefreshForShows(shows []model.Showtime) error {
	if !s.Configured() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tc, err := cache.LoadTMDB()
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	now := time.Now()

	for _, film := range scraper.UniqueFilms(shows) {
		key := scraper.NormalizeTitle(film.Title)
		if key == "" {
			continue
		}
		entry := tc.Entries[key]
		// Overview-only rows predate director/year in the cache; refresh them once.
		legacyEntry := entry.Overview != "" && entry.Director == "" && entry.ReleaseYear == ""
		if entryHasMetadata(entry) && !entry.FetchedAt.IsZero() && now.Sub(entry.FetchedAt) < 7*24*time.Hour && !legacyEntry && !shouldRefreshTMDB(entry, film) {
			continue
		}
		meta, err := searchMovie(client, s.APIKey, scraper.LookupTitle(film.Title), film.Year, film.Director)
		if err == nil {
			tc.Entries[key] = mergeTMDBEntry(entry, meta, now)
		}
	}
	if err := cache.SaveTMDB(tc); err != nil {
		return err
	}
	s.lookupMu.Lock()
	s.lookup = nil
	s.lookupMu.Unlock()
	return nil
}

// LookupAll returns the cached TMDB metadata keyed by normalized title. The
// result is memoized until tmdb.json changes on disk; callers must treat the
// map as read-only.
func (s *Service) LookupAll() (map[string]model.FilmTMDB, error) {
	s.lookupMu.Lock()
	defer s.lookupMu.Unlock()

	stamp := cache.TMDBStamp()
	if s.lookup != nil && stamp == s.lookupStat {
		return s.lookup, nil
	}

	tc, err := cache.LoadTMDB()
	if err != nil {
		return nil, err
	}
	out := map[string]model.FilmTMDB{}
	for key, entry := range tc.Entries {
		if !entryHasMetadata(entry) {
			continue
		}
		out[key] = model.FilmTMDB{
			PosterURL:   entry.PosterURL,
			Overview:    entry.Overview,
			Director:    entry.Director,
			ReleaseYear: entry.ReleaseYear,
		}
	}
	s.lookup = out
	s.lookupStat = stamp
	return out, nil
}

// mergeTMDBEntry folds freshly fetched metadata into the cached entry without
// clearing fields TMDB omitted. A result can arrive with an overview but no
// director when the details call fails, and overwriting on empty threw away
// good cached values.
func mergeTMDBEntry(entry model.TMDBEntry, meta *movieMeta, fetchedAt time.Time) model.TMDBEntry {
	entry.FetchedAt = fetchedAt
	if meta.PosterURL != "" {
		entry.PosterURL = meta.PosterURL
	}
	if meta.Overview != "" {
		entry.Overview = meta.Overview
	}
	if meta.Director != "" {
		entry.Director = meta.Director
	}
	if meta.ReleaseYear != "" {
		entry.ReleaseYear = meta.ReleaseYear
	}
	return entry
}

type movieMeta struct {
	PosterURL   string
	Overview    string
	Director    string
	ReleaseYear string
}

func entryHasMetadata(entry model.TMDBEntry) bool {
	return entry.PosterURL != "" || entry.Overview != "" || entry.Director != "" || entry.ReleaseYear != ""
}

func shouldRefreshTMDB(entry model.TMDBEntry, film model.Showtime) bool {
	if film.Year != "" && entry.ReleaseYear == "" {
		return true
	}
	if film.Director != "" && entry.Director == "" {
		return true
	}
	return hintMismatch(entry, film)
}

func hintMismatch(entry model.TMDBEntry, film model.Showtime) bool {
	if film.Year != "" && entry.ReleaseYear != "" && film.Year != entry.ReleaseYear {
		return true
	}
	if film.Director != "" && entry.Director != "" && !directorMatches(film.Director, entry.Director) {
		return true
	}
	return false
}

func searchMovie(client *http.Client, apiKey, title, year, director string) (*movieMeta, error) {
	q := url.Values{}
	q.Set("query", title)
	q.Set("api_key", apiKey)
	if year != "" {
		q.Set("primary_release_year", year)
	}
	resp, err := client.Get("https://api.themoviedb.org/3/search/movie?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			ID          int    `json:"id"`
			PosterPath  string `json:"poster_path"`
			Overview    string `json:"overview"`
			ReleaseDate string `json:"release_date"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Results) == 0 {
		return nil, fmt.Errorf("no results")
	}

	limit := len(payload.Results)
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	for i := 0; i < limit; i++ {
		r := payload.Results[i]
		if year != "" {
			if resultYear := yearFromDate(r.ReleaseDate); resultYear != "" && resultYear != year {
				continue
			}
		}
		meta, err := metaForResult(client, apiKey, r.ID, r.Overview, r.PosterPath, r.ReleaseDate)
		if err != nil {
			continue
		}
		if director != "" && !directorMatches(director, meta.Director) {
			continue
		}
		if year != "" && meta.ReleaseYear != "" && meta.ReleaseYear != year {
			continue
		}
		return meta, nil
	}

	if director != "" || year != "" {
		return nil, fmt.Errorf("no matching results")
	}

	r := payload.Results[0]
	return metaForResult(client, apiKey, r.ID, r.Overview, r.PosterPath, r.ReleaseDate)
}

func metaForResult(client *http.Client, apiKey string, id int, overview, posterPath, releaseDate string) (*movieMeta, error) {
	meta := movieMetaFromResult(overview, posterPath, releaseDate)
	directors, releaseYear, err := fetchMovieDetails(client, apiKey, id)
	if err == nil {
		meta.Director = directors
		if meta.ReleaseYear == "" {
			meta.ReleaseYear = releaseYear
		}
	}
	if meta.PosterURL == "" && meta.Overview == "" {
		return nil, fmt.Errorf("empty metadata")
	}
	return &meta, nil
}

func movieMetaFromResult(overview, posterPath, releaseDate string) movieMeta {
	meta := movieMeta{Overview: overview, ReleaseYear: yearFromDate(releaseDate)}
	if posterPath != "" {
		meta.PosterURL = posterBase + posterPath
	}
	return meta
}

func directorMatches(hint, tmdbDirectors string) bool {
	hintParts := splitDirectorNames(hint)
	tmdbParts := splitDirectorNames(tmdbDirectors)
	if len(hintParts) == 0 || len(tmdbParts) == 0 {
		return false
	}
	for _, h := range hintParts {
		for _, t := range tmdbParts {
			if h == t {
				return true
			}
			hLast, tLast := directorLastName(h), directorLastName(t)
			if len(hLast) >= 4 && hLast == tLast {
				return true
			}
		}
	}
	return false
}

func splitDirectorNames(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func directorLastName(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func fetchMovieDetails(client *http.Client, apiKey string, movieID int) (director, releaseYear string, err error) {
	u := fmt.Sprintf(
		"https://api.themoviedb.org/3/movie/%d?api_key=%s&append_to_response=credits",
		movieID,
		url.QueryEscape(apiKey),
	)
	resp, err := client.Get(u)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("details HTTP %d", resp.StatusCode)
	}
	var payload struct {
		ReleaseDate string `json:"release_date"`
		Credits     struct {
			Crew []struct {
				Name string `json:"name"`
				Job  string `json:"job"`
			} `json:"crew"`
		} `json:"credits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}
	releaseYear = yearFromDate(payload.ReleaseDate)
	var directors []string
	for _, c := range payload.Credits.Crew {
		if c.Job == "Director" {
			directors = append(directors, c.Name)
		}
	}
	return strings.Join(directors, ", "), releaseYear, nil
}

func yearFromDate(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}
