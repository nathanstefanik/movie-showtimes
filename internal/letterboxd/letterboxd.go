package letterboxd

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"movie-showtimes/internal/scraper"
)

const defaultDir = "data/movies"

var (
	yearInParens = regexp.MustCompile(`\((19|20)(\d{2})\)`)
	trailingYear = regexp.MustCompile(`\b((19|20)\d{2})\s*$`)
)

// csvFiles is the fixed set of Letterboxd exports the library reads.
var csvFiles = []string{"watched.csv", "watchlist.csv", "ratings.csv"}

// statInterval bounds how often the export files are stat-ed for changes.
// Status is called once per film per grid build, and re-stat-ing three files
// on every call turned rendering into a syscall storm.
const statInterval = 5 * time.Second

type Library struct {
	// mu guards every field below; Status is called concurrently from HTTP
	// handlers and the reload path rewrites the maps wholesale.
	mu       sync.Mutex
	dir      string
	films    map[string]*record
	byTitle  map[string][]string
	loadedAt time.Time
	lastStat time.Time
	mtimes   map[string]time.Time
}

type record struct {
	watched   bool
	watchlist bool
	rating    *float64
}

func New() *Library {
	return &Library{dir: resolveDataDir()}
}

func resolveDataDir() string {
	if dir := os.Getenv("LETTERBOXD_DATA_DIR"); dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return defaultDir
	}
	for {
		candidate := filepath.Join(wd, defaultDir)
		if _, err := os.Stat(filepath.Join(candidate, "watched.csv")); err == nil {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return defaultDir
}

func (l *Library) Configured() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.reloadIfStale(); err != nil {
		return false
	}
	return len(l.films) > 0
}

func (l *Library) FilmCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.reloadIfStale(); err != nil {
		return 0
	}
	return len(l.films)
}

func (l *Library) Status(title string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.reloadIfStale(); err != nil {
		return ""
	}
	rec := l.lookup(title)
	if rec == nil {
		return ""
	}
	return rec.status()
}

func (r *record) status() string {
	if r.rating != nil {
		if *r.rating >= 4 {
			return "loved"
		}
		if *r.rating <= 2 {
			return "disliked"
		}
	}
	if r.watched {
		return "watched"
	}
	if r.watchlist {
		return "watchlist"
	}
	return ""
}

func (l *Library) lookup(title string) *record {
	year := extractYear(title)
	base := scraper.NormalizeTitle(title)

	if year > 0 {
		return l.films[filmKey(base, year)]
	}
	keys := l.byTitle[base]
	if len(keys) == 1 {
		return l.films[keys[0]]
	}
	return nil
}

func filmKey(normTitle string, year int) string {
	return fmt.Sprintf("%s\x00%d", normTitle, year)
}

func extractYear(title string) int {
	if m := yearInParens.FindStringSubmatch(title); len(m) == 3 {
		y, _ := strconv.Atoi(m[1] + m[2])
		return y
	}
	if m := trailingYear.FindStringSubmatch(title); len(m) == 2 {
		y, _ := strconv.Atoi(m[1])
		return y
	}
	return 0
}

// reloadIfStale must be called with l.mu held.
func (l *Library) reloadIfStale() error {
	loaded := l.films != nil
	now := time.Now()
	if loaded && now.Sub(l.lastStat) < statInterval {
		return nil
	}
	l.lastStat = now

	stale := !loaded
	for _, name := range csvFiles {
		info, err := os.Stat(filepath.Join(l.dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !stale {
			if prev, ok := l.mtimes[name]; !ok || info.ModTime().After(prev) {
				stale = true
			}
		}
	}

	if !stale {
		return nil
	}
	return l.load(csvFiles)
}

func (l *Library) load(files []string) error {
	films := map[string]*record{}
	byTitle := map[string][]string{}

	for _, name := range files {
		path := filepath.Join(l.dir, name)
		table, err := readCSV(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if table == nil {
			continue
		}
		for _, row := range table.rows {
			switch name {
			case "watched.csv":
				mergeRow(films, byTitle, table, row, func(r *record) { r.watched = true })
			case "watchlist.csv":
				mergeRow(films, byTitle, table, row, func(r *record) { r.watchlist = true })
			case "ratings.csv":
				rating, err := strconv.ParseFloat(strings.TrimSpace(table.field(row, "Rating")), 64)
				if err != nil {
					continue
				}
				mergeRow(films, byTitle, table, row, func(r *record) { r.rating = &rating })
			}
		}
	}

	l.films = films
	l.byTitle = byTitle
	l.loadedAt = time.Now()
	l.mtimes = map[string]time.Time{}
	for _, name := range files {
		if info, err := os.Stat(filepath.Join(l.dir, name)); err == nil {
			l.mtimes[name] = info.ModTime()
		}
	}
	return nil
}

func mergeRow(films map[string]*record, byTitle map[string][]string, t *table, row []string, apply func(*record)) {
	name := strings.TrimSpace(t.field(row, "Name"))
	yearStr := strings.TrimSpace(t.field(row, "Year"))
	if name == "" || yearStr == "" {
		return
	}
	year, err := strconv.Atoi(yearStr)
	if err != nil || year <= 0 {
		return
	}
	norm := scraper.NormalizeTitle(name)
	key := filmKey(norm, year)
	rec, ok := films[key]
	if !ok {
		rec = &record{}
		films[key] = rec
		if !contains(byTitle[norm], key) {
			byTitle[norm] = append(byTitle[norm], key)
		}
	}
	apply(rec)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// table keeps rows as the raw slices csv.Reader already allocated and resolves
// columns by index. The previous version built a map[string]string per row,
// which allocated a map and a dozen strings for every line of the export.
type table struct {
	col  map[string]int
	rows [][]string
}

func (t *table) field(row []string, name string) string {
	idx, ok := t.col[name]
	if !ok || idx >= len(row) {
		return ""
	}
	return row[idx]
}

func readCSV(path string) (*table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.ReuseRecord = false
	all, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}

	col := map[string]int{}
	for i, h := range all[0] {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}

	rows := all[1:]
	kept := rows[:0]
	for _, line := range rows {
		if len(line) == 0 || strings.TrimSpace(line[0]) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return &table{col: col, rows: kept}, nil
}
