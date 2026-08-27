package scraper

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"movie-showtimes/internal/model"
)

type FilmlincParser struct{}

var flcDateRe = regexp.MustCompile(`(?i)^(Mon|Tue|Wed|Thu|Fri|Sat|Sun),?\s+(\w+)\s+(\d{1,2})$`)
var flcTimeRe = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})\s*(AM|PM)`)
var flcYearPipeRe = regexp.MustCompile(`^(\d{4})\|`)
var flcFormatLineRe = regexp.MustCompile(`(?i)^(70mm|35mm|16mm|imax)$`)

func (FilmlincParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	doc, err := FetchDoc(ctx, client, "https://www.filmlinc.org/now-playing/")
	if err != nil {
		return nil, err
	}
	if strings.Contains(doc.Find("title").Text(), "Just a moment") {
		return nil, fmt.Errorf("blocked by site protection (Cloudflare)")
	}

	filmURLs := map[string]string{}
	cardMeta := map[string]FilmMeta{}
	out := parseFilmlincCards(doc, theater, filmURLs, cardMeta)
	if len(out) == 0 {
		doc.Find("h2").Each(func(_ int, h *goquery.Selection) {
			title := strings.TrimSpace(h.Text())
			if title == "" || strings.EqualFold(title, "Now Playing at FLC") || strings.EqualFold(title, "What's On") {
				return
			}
			block := h.Parent()
			if block.Length() == 0 {
				block = h
			}
			shows := parseFilmlincShowtimes(title, block.Text(), theater)
			out = append(out, shows...)
		})
	}

	if len(out) == 0 {
		out = parseFilmlincFlat(doc, theater)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes found on now-playing page")
	}
	out = dedupeShowtimes(out)
	ApplyFilmURLs(out, filmURLs)
	ApplyFilmMeta(out, cardMeta)
	EnrichFilmMetaFromURLs(ctx, client, out, filmURLs, ParseFilmlincFilmMeta)
	return out, nil
}

func parseFilmlincCards(doc *goquery.Document, theater model.Theater, filmURLs map[string]string, cardMeta map[string]FilmMeta) []model.Showtime {
	var out []model.Showtime
	doc.Find(`div.py-8.lg\:py-10.border-b.border-border`).Each(func(_ int, block *goquery.Selection) {
		titleLink := block.Find(`a[href*="/films/"]`).First()
		title := strings.TrimSpace(titleLink.Find(`div[data-typography-desktop="h6-body-medium"]`).First().Text())
		if title == "" {
			title = strings.TrimSpace(titleLink.Find("div").First().Text())
		}
		if title == "" || strings.EqualFold(title, "Read More") {
			return
		}
		displayTitle := DisplayTitle(title)
		key := NormalizeTitle(displayTitle)
		if href, ok := titleLink.Attr("href"); ok {
			filmURLs[key] = AbsoluteURL(href, "https://www.filmlinc.org")
		}
		if meta := parseFilmlincCardMeta(block, displayTitle); meta.Director != "" || meta.Year != "" || meta.Overview != "" {
			cardMeta[key] = meta
		}
		block.Find(`p[data-typography-desktop="eyebrow-sm"]`).Each(func(_ int, dateEl *goquery.Selection) {
			m := flcDateRe.FindStringSubmatch(strings.TrimSpace(dateEl.Text()))
			if m == nil {
				return
			}
			dateStr := parseFilmlincDate(m[2], m[3])
			if dateStr == "" || !InWindow(dateStr) {
				return
			}
			seen := map[string]struct{}{}
			dateEl.Parent().Find(`button[data-performance-id]`).Each(func(_ int, btn *goquery.Selection) {
				tm := strings.TrimSpace(btn.Find("span").First().Text())
				sub := flcTimeRe.FindStringSubmatch(tm)
				if sub == nil {
					return
				}
				t := format24(sub[1], sub[2], sub[3])
				key := dateStr + "|" + t
				if _, ok := seen[key]; ok {
					return
				}
				seen[key] = struct{}{}
				out = append(out, model.Showtime{
					TheaterID:   theater.ID,
					TheaterName: theater.Name,
					Title:       displayTitle,
					Date:        dateStr,
					Time:        t,
				})
			})
		})
	})
	return out
}

func parseFilmlincCardMeta(block *goquery.Selection, displayTitle string) FilmMeta {
	var meta FilmMeta
	meta.Director = strings.TrimSpace(block.Find(`p[data-typography-desktop="body-sm"]`).First().Text())
	block.Find(`p[data-typography-desktop="body-xs"]`).Each(func(_ int, p *goquery.Selection) {
		if meta.Year != "" {
			return
		}
		if m := flcYearPipeRe.FindStringSubmatch(strings.TrimSpace(p.Text())); m != nil {
			meta.Year = m[1]
		}
	})
	block.Find("p").Each(func(_ int, p *goquery.Selection) {
		if _, ok := p.Attr("data-typography-desktop"); ok {
			return
		}
		text := strings.TrimSpace(p.Text())
		if len(text) > 80 && meta.Overview == "" {
			meta.Overview = text
		}
	})
	if meta.Director != "" && !looksLikeFilmlincDirectorLine(meta.Director) {
		meta.Director = ""
	}
	return meta
}

func ParseFilmlincFilmMeta(doc *goquery.Document) FilmMeta {
	var meta FilmMeta
	meta.Director = strings.TrimSpace(doc.Find(`div[data-typography-desktop="h9-body"]`).First().Text())
	doc.Find(`div[data-typography-desktop="eyebrow-md"]`).Each(func(_ int, label *goquery.Selection) {
		key := strings.ToUpper(strings.TrimSpace(label.Text()))
		value := strings.TrimSpace(label.Next().Text())
		switch key {
		case "DIRECTOR":
			if meta.Director == "" {
				meta.Director = value
			}
		case "YEAR":
			if filmYearRe.MatchString(value) {
				meta.Year = value
			}
		}
	})
	if meta.Overview == "" {
		meta.Overview = filmlincOverviewFromDoc(doc)
	}
	return meta
}

func filmlincOverviewFromDoc(doc *goquery.Document) string {
	var best string
	doc.Find("p").Each(func(_ int, p *goquery.Selection) {
		if _, ok := p.Attr("data-typography-desktop"); ok {
			return
		}
		text := strings.TrimSpace(p.Text())
		if len(text) < 80 || strings.HasPrefix(text, "Filmed in") || strings.HasPrefix(text, "Please note") {
			return
		}
		if best == "" || len(text) < len(best) {
			best = text
		}
	})
	return best
}

func looksLikeFilmlincDirectorLine(line string) bool {
	if line == "" || flcDateRe.MatchString(line) || flcTimeRe.MatchString(line) {
		return false
	}
	if flcFormatLineRe.MatchString(line) || filmYearRe.MatchString(line) {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "premiere") || strings.Contains(lower, "festival") ||
		strings.Contains(lower, "showtime") || strings.EqualFold(line, "Read More") {
		return false
	}
	if strings.Contains(line, "|") || len(line) > 60 {
		return false
	}
	return true
}

func parseFilmlincFlat(doc *goquery.Document, theater model.Theater) []model.Showtime {
	text := doc.Text()
	lines := splitLines(text)
	var (
		out          []model.Showtime
		currentTitle string
		currentDate  string
	)
	for _, line := range lines {
		if strings.EqualFold(line, "Showtimes") {
			continue
		}
		if m := flcDateRe.FindStringSubmatch(line); m != nil {
			currentDate = parseFilmlincDate(m[2], m[3])
			continue
		}
		if m := flcTimeRe.FindStringSubmatch(line); m != nil {
			if currentTitle == "" || currentDate == "" || !InWindow(currentDate) {
				continue
			}
			t := format24(m[1], m[2], m[3])
			out = append(out, model.Showtime{
				TheaterID:   theater.ID,
				TheaterName: theater.Name,
				Title:       DisplayTitle(currentTitle),
				Date:        currentDate,
				Time:        t,
			})
			continue
		}
		if len(line) > 3 && !strings.Contains(line, "|") && !strings.EqualFold(line, "Read More") &&
			!flcTimeRe.MatchString(line) && !flcDateRe.MatchString(line) &&
			!strings.Contains(strings.ToLower(line), "minutes") &&
			!strings.Contains(strings.ToLower(line), "premiere") {
			if currentTitle == "" || strings.HasPrefix(strings.ToLower(line), "showtimes") {
				continue
			}
		}
		if isFilmlincTitleLine(line) {
			currentTitle = line
			currentDate = ""
		}
	}
	return out
}

func parseFilmlincShowtimes(title, body string, theater model.Theater) []model.Showtime {
	lines := splitLines(body)
	idx := -1
	for i, line := range lines {
		if strings.EqualFold(line, "Showtimes") {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	var (
		out         []model.Showtime
		currentDate string
		seenTimes   = map[string]struct{}{}
	)
	for _, line := range lines[idx+1:] {
		if isFilmlincTitleLine(line) && !strings.EqualFold(line, title) {
			break
		}
		if m := flcDateRe.FindStringSubmatch(line); m != nil {
			currentDate = parseFilmlincDate(m[2], m[3])
			seenTimes = map[string]struct{}{}
			continue
		}
		if m := flcTimeRe.FindStringSubmatch(line); m != nil {
			if currentDate == "" || !InWindow(currentDate) {
				continue
			}
			t := format24(m[1], m[2], m[3])
			key := currentDate + "|" + t
			if _, ok := seenTimes[key]; ok {
				continue
			}
			seenTimes[key] = struct{}{}
			out = append(out, model.Showtime{
				TheaterID:   theater.ID,
				TheaterName: theater.Name,
				Title:       DisplayTitle(title),
				Date:        currentDate,
				Time:        t,
			})
		}
	}
	return out
}

func isFilmlincTitleLine(line string) bool {
	if line == "" || len(line) < 2 {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "showtime") || strings.Contains(lower, "minute") {
		return false
	}
	if flcDateRe.MatchString(line) || flcTimeRe.MatchString(line) {
		return false
	}
	if strings.Contains(line, "|") {
		return false
	}
	return true
}

func parseFilmlincDate(monthName, dayStr string) string {
	now := time.Now().In(NYC())
	for _, layout := range []string{"January 2", "Jan 2"} {
		t, err := time.ParseInLocation(layout, monthName+" "+dayStr, NYC())
		if err != nil {
			continue
		}
		year := now.Year()
		candidate := time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, NYC())
		if candidate.Before(now.AddDate(0, 0, -30)) {
			candidate = candidate.AddDate(1, 0, 0)
		}
		return candidate.Format("2006-01-02")
	}
	return ""
}
