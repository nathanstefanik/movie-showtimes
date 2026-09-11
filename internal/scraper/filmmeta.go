package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"movie-showtimes/internal/model"
)

var filmYearRe = regexp.MustCompile(`^\d{4}$`)

var metroDirectorRe = regexp.MustCompile(`(?i)^Director:\s*(.+)$`)
var metroYearRe = regexp.MustCompile(`^(\d{4})\s*/`)
var roxyYearRe = regexp.MustCompile(`\|\s*((19|20)\d{2})\s*\|`)
var ffDirectedRe = regexp.MustCompile(`(?i)(?:Written and )?[Dd]irected by\s+([^<\n]+)`)
var ffMetaLineRe = regexp.MustCompile(`(?i)(directed by|\b(?:19|20)\d{2}\b.{0,20}\bmin)`)
var ffYearRe = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
var bamDirectedRe = regexp.MustCompile(`(?i)Directed by\s+([^(\n]+)`)
var bamYearRe = regexp.MustCompile(`\((\d{4})\)`)
var afaByRe = regexp.MustCompile(`(?i)^by\s+(.+)$`)

// The year is not always at the start of its line, and ranges are written both
// ways: "In Portuguese with English subtitles, 2000, 171 min", "With Danish
// intertitles, 1927-28, 98 min", "2024/25, 98 min". Match anywhere as long as
// it directly precedes the runtime; the film's own metadata line comes before
// its notes, so the first match (and the range's first year) is the right one.
var afaYearLineRe = regexp.MustCompile(`(\d{4})(?:[-/]\d{2,4})?,\s*\d+\s*min`)

type FilmMeta struct {
	Director string
	Year     string
	Overview string
}

func ParseLabeledFilmMeta(doc *goquery.Document) FilmMeta {
	lines := splitLines(doc.Text())
	var director, year string
	for i, line := range lines {
		switch strings.ToUpper(line) {
		case "DIRECTOR", "DIRECTED BY":
			if i+1 < len(lines) {
				director = strings.TrimSpace(lines[i+1])
			}
		case "YEAR", "RELEASE YEAR":
			if i+1 < len(lines) {
				candidate := strings.TrimSpace(lines[i+1])
				if filmYearRe.MatchString(candidate) {
					year = candidate
				}
			}
		}
	}
	return FilmMeta{Director: director, Year: year}
}

type FilmMetaParser func(*goquery.Document) FilmMeta

func ParseMetrographFilmMeta(doc *goquery.Document) FilmMeta {
	var director, year string
	doc.Find("h5").Each(func(_ int, h *goquery.Selection) {
		text := strings.TrimSpace(h.Text())
		if m := metroDirectorRe.FindStringSubmatch(text); m != nil && director == "" {
			director = strings.TrimSpace(m[1])
		}
		if year == "" {
			if m := metroYearRe.FindStringSubmatch(text); m != nil {
				year = m[1]
			}
		}
	})
	overview := strings.TrimSpace(doc.Find("div.film-copy, div.film-description").First().Text())
	if len(overview) > 500 {
		overview = overview[:500]
	}
	return FilmMeta{Director: director, Year: year, Overview: overview}
}

func ParseRoxyFilmMeta(doc *goquery.Document) FilmMeta {
	var director string
	doc.Find("p.event-info__header").Each(func(_ int, h *goquery.Selection) {
		if !strings.EqualFold(strings.TrimSpace(h.Text()), "Director") {
			return
		}
		director = strings.TrimSpace(h.Next().Text())
	})
	year := ""
	if m := roxyYearRe.FindStringSubmatch(doc.Text()); m != nil {
		year = m[1]
	}
	overview := strings.TrimSpace(doc.Find("div.event-info__description, div.screening__description").First().Text())
	return FilmMeta{Director: director, Year: year, Overview: overview}
}

func ParseFilmForumFilmMeta(doc *goquery.Document) FilmMeta {
	var meta FilmMeta
	// The director credit lives in div.urgent for new releases; repertory
	// titles carry a bold credit block ("Japan, 1964 / Directed by ..." or
	// "2025 96 MIN. USA") inside div.copy. Searching the whole page instead
	// picks up synopsis prose like "written and directed by Sandor Stern,
	// screenwriter of ..." and long sentences become the director.
	urgent := strings.TrimSpace(doc.Find("div.urgent").First().Text())
	if m := ffDirectedRe.FindStringSubmatch(urgent); m != nil {
		meta.Director = strings.TrimSpace(m[1])
	}
	doc.Find("div.copy strong").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text := strings.TrimSpace(s.Text())
		if !ffMetaLineRe.MatchString(text) {
			return true
		}
		if meta.Director == "" {
			if m := ffDirectedRe.FindStringSubmatch(text); m != nil {
				meta.Director = strings.TrimSpace(m[1])
			}
		}
		if m := ffYearRe.FindStringSubmatch(text); m != nil {
			meta.Year = m[1]
		}
		return false
	})
	overview := strings.TrimSpace(doc.Find("div.film-description, div#film-description").First().Text())
	return FilmMeta{Director: meta.Director, Year: meta.Year, Overview: overview}
}

func ParseBAMFilmMeta(doc *goquery.Document) FilmMeta {
	text := strings.TrimSpace(doc.Find("div.directedByText").First().Text())
	var director, year string
	if m := bamDirectedRe.FindStringSubmatch(text); m != nil {
		director = strings.TrimSpace(m[1])
	}
	if m := bamYearRe.FindStringSubmatch(text); m != nil {
		year = m[1]
	}
	return FilmMeta{Director: director, Year: year}
}

func parseAnthologyListMeta(doc *goquery.Document) map[string]FilmMeta {
	metaByTitle := map[string]FilmMeta{}
	doc.Find("div.film-showing").Each(func(_ int, block *goquery.Selection) {
		title := strings.TrimSpace(block.Find("span.film-title").First().Text())
		if title == "" {
			return
		}
		key := NormalizeTitle(DisplayTitle(title))
		details := strings.TrimSpace(block.Find("div.showing-details").First().Text())
		var director, year string
		for _, line := range splitLines(details) {
			if director == "" {
				if m := afaByRe.FindStringSubmatch(line); m != nil {
					director = strings.TrimSpace(m[1])
				}
			}
			if year == "" {
				if m := afaYearLineRe.FindStringSubmatch(line); m != nil {
					year = m[1]
				}
			}
		}
		if director == "" && year == "" {
			return
		}
		metaByTitle[key] = FilmMeta{Director: director, Year: year}
	})
	return metaByTitle
}

func EnrichAnthologyFilmMeta(ctx context.Context, client *http.Client, shows []model.Showtime, start, end time.Time) {
	metaByTitle := map[string]FilmMeta{}
	cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, NYC())
	last := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, NYC())
	for !cur.After(last) {
		url := fmt.Sprintf(
			"https://www.anthologyfilmarchives.org/film_screenings/calendar?view=list&month=%02d&year=%d",
			cur.Month(), cur.Year(),
		)
		doc, err := FetchDoc(ctx, client, url)
		if err == nil {
			for key, meta := range parseAnthologyListMeta(doc) {
				metaByTitle[key] = meta
			}
		}
		cur = cur.AddDate(0, 1, 0)
	}
	ApplyFilmMeta(shows, metaByTitle)
}

func EnrichFilmMetaFromURLs(ctx context.Context, client *http.Client, shows []model.Showtime, filmURLs map[string]string, parse FilmMetaParser) {
	if parse == nil {
		parse = ParseLabeledFilmMeta
	}
	metaByTitle := map[string]FilmMeta{}
	for key, filmURL := range filmURLs {
		if showHasFullMeta(shows, key) {
			continue
		}
		doc, err := FetchDoc(ctx, client, filmURL)
		if err != nil {
			continue
		}
		if strings.Contains(doc.Find("title").Text(), "Just a moment") {
			continue
		}
		meta := parse(doc)
		if meta.Director == "" && meta.Year == "" && meta.Overview == "" {
			continue
		}
		metaByTitle[key] = meta
	}
	ApplyFilmMeta(shows, metaByTitle)
}

func showHasFullMeta(shows []model.Showtime, key string) bool {
	found := false
	for _, s := range shows {
		if NormalizeTitle(s.Title) != key {
			continue
		}
		found = true
		if s.Director == "" || s.Year == "" || s.Overview == "" {
			return false
		}
	}
	return found
}

func ApplyFilmMeta(shows []model.Showtime, metaByTitle map[string]FilmMeta) {
	for i := range shows {
		key := NormalizeTitle(shows[i].Title)
		meta, ok := metaByTitle[key]
		if !ok {
			continue
		}
		if shows[i].Director == "" && meta.Director != "" {
			shows[i].Director = meta.Director
		}
		if shows[i].Year == "" && meta.Year != "" {
			shows[i].Year = meta.Year
		}
		if shows[i].Overview == "" && meta.Overview != "" {
			shows[i].Overview = meta.Overview
		}
	}
}

func ApplyFilmURLs(shows []model.Showtime, filmURLs map[string]string) {
	for i := range shows {
		if shows[i].FilmURL != "" {
			continue
		}
		if u, ok := filmURLs[NormalizeTitle(shows[i].Title)]; ok {
			shows[i].FilmURL = u
		}
	}
}

func AbsoluteURL(href, base string) string {
	if href == "" || base == "" {
		return ""
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := baseURL.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	if abs.Host == "" || abs.User != nil {
		return ""
	}
	return abs.String()
}

func splitLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func dedupeShowtimes(in []model.Showtime) []model.Showtime {
	seen := map[string]struct{}{}
	out := make([]model.Showtime, 0, len(in))
	for _, s := range in {
		key := s.TheaterID + "|" + s.Date + "|" + s.Time + "|" + NormalizeTitle(s.Title)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
