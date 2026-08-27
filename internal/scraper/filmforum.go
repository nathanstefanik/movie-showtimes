package scraper

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"movie-showtimes/internal/model"
)

type FilmForumParser struct{}

var ffTabIDRe = regexp.MustCompile(`^tabs-(\d+)$`)

func (FilmForumParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	doc, err := FetchDoc(ctx, client, "https://filmforum.org/now_playing")
	if err != nil {
		return nil, err
	}
	filmURLs := map[string]string{}
	out := parseFilmForumShowtimes(doc, theater, filmURLs)
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes found on now playing page")
	}
	out = dedupeShowtimes(out)
	ApplyFilmURLs(out, filmURLs)
	EnrichFilmMetaFromURLs(ctx, client, out, filmURLs, ParseFilmForumFilmMeta)
	return out, nil
}

// filmForumWeekStart is the date of tabs-0. Film Forum used to anchor the tab
// strip to the Friday that opened the week; it now runs a rolling seven days
// starting today (the tab labels read TUE WED THU… from the current weekday).
func filmForumWeekStart() time.Time {
	now := time.Now().In(NYC())
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, NYC())
}

func parseFilmForumShowtimes(doc *goquery.Document, theater model.Theater, filmURLs map[string]string) []model.Showtime {
	weekStart := filmForumWeekStart()
	var out []model.Showtime
	doc.Find(`div[id^="tabs-"]`).Each(func(_ int, tab *goquery.Selection) {
		id, ok := tab.Attr("id")
		if !ok {
			return
		}
		m := ffTabIDRe.FindStringSubmatch(id)
		if m == nil {
			return
		}
		idx, _ := strconv.Atoi(m[1])
		dateStr := weekStart.AddDate(0, 0, idx).Format("2006-01-02")
		if !InWindow(dateStr) {
			return
		}
		tab.Find("p").Each(func(_ int, p *goquery.Selection) {
			titleLink := p.Find("strong a").First()
			title := strings.TrimSpace(titleLink.Text())
			if title == "" {
				title = strings.TrimSpace(p.Find("strong").First().Text())
			}
			if title == "" {
				return
			}
			displayTitle := DisplayTitle(title)
			if href, ok := titleLink.Attr("href"); ok {
				filmURLs[NormalizeTitle(displayTitle)] = AbsoluteURL(href, "https://filmforum.org")
			}
			p.Find("span").Each(func(_ int, span *goquery.Selection) {
				if span.HasClass("alert") {
					return
				}
				raw := strings.TrimSpace(span.Text())
				if !strings.Contains(raw, ":") {
					return
				}
				t := normalizeFilmForumTime(raw)
				if t == "" {
					return
				}
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

func normalizeFilmForumTime(raw string) string {
	s := strings.TrimSpace(raw)
	if m := timeRe.FindStringSubmatch(s); m != nil {
		return format24(m[1], m[2], m[3])
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return ""
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return ""
	}
	min := parts[1]
	if h == 12 {
		return fmt.Sprintf("12:%s", min)
	}
	if h >= 10 && h <= 11 {
		return fmt.Sprintf("%02d:%s", h, min)
	}
	if h >= 1 && h <= 9 {
		h += 12
	}
	return fmt.Sprintf("%02d:%s", h, min)
}
