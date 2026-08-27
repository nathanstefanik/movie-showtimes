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

type MetrographParser struct{}

func (MetrographParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	doc, err := FetchDoc(ctx, client, "https://metrograph.com/calendar/")
	if err != nil {
		return nil, err
	}
	filmURLs := map[string]string{}
	shows := parseMetrographCalendar(doc, theater, filmURLs)
	if len(shows) > 0 {
		ApplyFilmURLs(shows, filmURLs)
		EnrichFilmMetaFromURLs(ctx, client, shows, filmURLs, ParseMetrographFilmMeta)
		return shows, nil
	}
	return fetchMetrographFilmPages(ctx, client, doc, theater)
}

func parseMetrographCalendar(doc *goquery.Document, theater model.Theater, filmURLs map[string]string) []model.Showtime {
	var out []model.Showtime
	doc.Find(`div.calendar-list-day[id^="calendar-list-day-"]`).Each(func(_ int, day *goquery.Selection) {
		id, ok := day.Attr("id")
		if !ok {
			return
		}
		dateStr := strings.TrimPrefix(id, "calendar-list-day-")
		if !InWindow(dateStr) {
			return
		}
		day.Find("div.item.film-thumbnail").Each(func(_ int, item *goquery.Selection) {
			titleLink := item.Find("h4 a.title").First()
			title := strings.TrimSpace(titleLink.Text())
			if title == "" {
				title = strings.TrimSpace(item.Find("h4").First().Text())
			}
			if title == "" {
				return
			}
			displayTitle := DisplayTitle(title)
			if href, ok := titleLink.Attr("href"); ok {
				filmURLs[NormalizeTitle(displayTitle)] = AbsoluteURL(href, "https://metrograph.com")
			}
			item.Find("div.showtimes a").Each(func(_ int, a *goquery.Selection) {
				t := NormalizeTime(a.Text())
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

func fetchMetrographFilmPages(ctx context.Context, client *http.Client, doc *goquery.Document, theater model.Theater) ([]model.Showtime, error) {
	seen := map[string]struct{}{}
	var filmURLs []string
	doc.Find(`a[href*="vista_film_id"]`).Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok {
			return
		}
		key := AbsoluteURL(href, "https://metrograph.com")
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		filmURLs = append(filmURLs, key)
	})

	var out []model.Showtime
	dateHeaderRe := regexp.MustCompile(`(?i)(Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)\s+(\w+)\s+(\d{1,2})`)
	timeLinkRe := regexp.MustCompile(`(?i)^\d{1,2}:\d{2}\s*(am|pm)$`)

	for _, filmURL := range filmURLs {
		fdoc, err := FetchDoc(ctx, client, filmURL)
		if err != nil {
			continue
		}
		title := strings.TrimSpace(fdoc.Find("h1").First().Text())
		if title == "" {
			title = strings.TrimSpace(fdoc.Find("h2.film-title").First().Text())
		}
		if title == "" {
			continue
		}
		displayTitle := DisplayTitle(title)
		meta := ParseMetrographFilmMeta(fdoc)
		var currentDate string
		fdoc.Find("h5, h4, .showtimes a, .film-showtimes a").Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if m := dateHeaderRe.FindStringSubmatch(text); m != nil {
				currentDate = parseMetrographDateHeader(m[2], m[3])
				return
			}
			if currentDate == "" || !InWindow(currentDate) {
				return
			}
			if timeLinkRe.MatchString(text) || timeLinkRe.MatchString(NormalizeTime(text)) {
				t := NormalizeTime(text)
				out = append(out, model.Showtime{
					TheaterID:   theater.ID,
					TheaterName: theater.Name,
					Title:       displayTitle,
					Director:    meta.Director,
					Year:        meta.Year,
					Overview:    meta.Overview,
					FilmURL:     filmURL,
					Date:        currentDate,
					Time:        t,
				})
			}
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes parsed from calendar or film pages")
	}
	return out, nil
}

func parseMetrographDateHeader(monthName, dayStr string) string {
	now := time.Now().In(NYC())
	for _, layout := range []string{"January 2", "Jan 2"} {
		t, err := time.ParseInLocation(layout, monthName+" "+dayStr, NYC())
		if err != nil {
			continue
		}
		year := now.Year()
		candidate := time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, NYC())
		if candidate.Before(now.AddDate(0, 0, -60)) {
			candidate = candidate.AddDate(1, 0, 0)
		}
		return candidate.Format("2006-01-02")
	}
	return ""
}
