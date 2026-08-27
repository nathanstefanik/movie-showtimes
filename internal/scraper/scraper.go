package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"movie-showtimes/internal/model"
)

type Parser interface {
	Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error)
}

var registry = map[string]Parser{
	"metrograph": &MetrographParser{},
	"filmlinc":   &FilmlincParser{},
	"roxy":       &RoxyParser{},
	"filmforum":  &FilmForumParser{},
	"anthology":  &AnthologyParser{},
	"bam":        &BAMParser{},
}

func Get(name string) (Parser, bool) {
	p, ok := registry[name]
	return p, ok
}

func FetchAll(ctx context.Context, client *http.Client, theaters []model.Theater) ([]model.Showtime, map[string]string) {
	var (
		mu     sync.Mutex
		all    []model.Showtime
		errors = map[string]string{}
		wg     sync.WaitGroup
	)

	for _, th := range theaters {
		th := th
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctxT, cancel := context.WithTimeout(ctx, TheaterFetchTimeout)
			defer cancel()

			if th.Parser == "" {
				mu.Lock()
				errors[th.ID] = "no parser configured"
				mu.Unlock()
				return
			}
			p, ok := Get(th.Parser)
			if !ok {
				mu.Lock()
				errors[th.ID] = "no parser configured"
				mu.Unlock()
				return
			}
			shows, err := p.Fetch(ctxT, client, th)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors[th.ID] = err.Error()
				return
			}
			all = append(all, shows...)
		}()
	}
	wg.Wait()
	return all, errors
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var nyLoc *time.Location

func init() {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("EST", -5*3600)
	}
	nyLoc = loc
}

func NYC() *time.Location {
	return nyLoc
}

func DefaultClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

func Window() (time.Time, time.Time) {
	now := time.Now().In(nyLoc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, nyLoc)
	end := start.AddDate(0, 0, 13)
	return start, end
}

// WindowDates returns the window bounds already formatted as YYYY-MM-DD.
// That layout sorts lexicographically, so callers filtering a large slice can
// compare strings instead of parsing every date.
func WindowDates() (string, string) {
	start, end := Window()
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

func InWindow(dateStr string) bool {
	start, end := WindowDates()
	return InWindowRange(dateStr, start, end)
}

func InWindowRange(dateStr, start, end string) bool {
	if len(dateStr) != len(start) {
		return false
	}
	return dateStr >= start && dateStr <= end
}

func FetchDoc(ctx context.Context, client *http.Client, url string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

var timeRe = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})\s*(am|pm)$`)
var compactTimeRe = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})(am|pm)$`)
var time24Re = regexp.MustCompile(`^\d{2}:\d{2}$`)

func NormalizeTime(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "@")
	s = strings.TrimSpace(s)
	if time24Re.MatchString(s) {
		return s
	}
	if m := timeRe.FindStringSubmatch(s); m != nil {
		return format24(m[1], m[2], m[3])
	}
	if m := compactTimeRe.FindStringSubmatch(s); m != nil {
		return format24(m[1], m[2], m[3])
	}
	return s
}

func format24(hour, min, ampm string) string {
	h, _ := strconv.Atoi(hour)
	switch strings.ToLower(ampm) {
	case "pm":
		if h != 12 {
			h += 12
		}
	case "am":
		if h == 12 {
			h = 0
		}
	}
	return fmt.Sprintf("%02d:%s", h, min)
}

func ShowtimeMinutes(t string) int {
	t = NormalizeTime(t)
	parts := strings.Split(t, ":")
	if len(parts) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h*60 + m
}

func CompareShowtimeTime(a, b string) int {
	ma, mb := ShowtimeMinutes(a), ShowtimeMinutes(b)
	if ma < mb {
		return -1
	}
	if ma > mb {
		return 1
	}
	return 0
}

var bracketRe = regexp.MustCompile(`\s*\[[^\]]*\]\s*$`)
var pipeTagRe = regexp.MustCompile(`\s*\|\s*[^|]+$`)
var dashTagRe = regexp.MustCompile(`\s*-\s*(35MM|35mm|New 4K[^|]*|Q&A.*)$`)
var formatSuffixRe = regexp.MustCompile(`(?i)\s+on\s+(70mm|35mm|16mm|imax)\s*$`)
var parenRe = regexp.MustCompile(`\s*\([^)]*\)`)

const TheaterFetchTimeout = 5 * time.Minute

func DisplayTitle(title string) string {
	t := strings.TrimSpace(title)
	t = bracketRe.ReplaceAllString(t, "")
	t = pipeTagRe.ReplaceAllString(t, "")
	t = dashTagRe.ReplaceAllString(t, "")
	return strings.TrimSpace(t)
}

func LookupTitle(title string) string {
	t := DisplayTitle(title)
	if i := strings.Index(t, "/"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if i := strings.Index(strings.ToLower(t), " preceded by "); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if parts := strings.Split(t, "+"); len(parts) > 1 {
		t = strings.TrimSpace(parts[0])
	}
	t = parenRe.ReplaceAllString(t, "")
	t = formatSuffixRe.ReplaceAllString(t, "")
	return strings.TrimSpace(t)
}

// Scraped titles repeat heavily (one film, many showtimes) and every lookup
// costs five regex passes, so memoize. The cache is cleared rather than grown
// without bound in the unlikely event a scraper starts emitting unique titles.
const maxTitleCacheEntries = 4096

var (
	titleCacheMu sync.Mutex
	titleCache   = map[string]string{}
)

func NormalizeTitle(title string) string {
	titleCacheMu.Lock()
	if v, ok := titleCache[title]; ok {
		titleCacheMu.Unlock()
		return v
	}
	titleCacheMu.Unlock()

	v := strings.ToLower(LookupTitle(title))

	titleCacheMu.Lock()
	if len(titleCache) >= maxTitleCacheEntries {
		titleCache = map[string]string{}
	}
	titleCache[title] = v
	titleCacheMu.Unlock()
	return v
}

func UniqueFilms(shows []model.Showtime) []model.Showtime {
	seen := map[string]int{}
	var out []model.Showtime
	for _, s := range shows {
		key := NormalizeTitle(s.Title)
		if key == "" {
			continue
		}
		if idx, ok := seen[key]; ok {
			if out[idx].Director == "" && s.Director != "" {
				out[idx].Director = s.Director
			}
			if out[idx].Year == "" && s.Year != "" {
				out[idx].Year = s.Year
			}
			if out[idx].Overview == "" && s.Overview != "" {
				out[idx].Overview = s.Overview
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, s)
	}
	return out
}

func TMDBSearchURL(title string) string {
	return "https://www.themoviedb.org/search/movie?query=" + url.QueryEscape(LookupTitle(title))
}

func WeekGridDates() []time.Time {
	start, end := Window()
	var dates []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d)
	}
	return dates
}

func FormatDateLabel(t time.Time) string {
	return t.Format("Mon 1/2")
}
