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

type AnthologyParser struct{}

var afaTimeRe = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})\s*(AM|PM)`)

func (AnthologyParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	start, end := Window()
	var out []model.Showtime
	cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, NYC())
	last := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, NYC())
	for !cur.After(last) {
		url := fmt.Sprintf(
			"https://www.anthologyfilmarchives.org/film_screenings/calendar?month=%02d&year=%d",
			cur.Month(), cur.Year(),
		)
		doc, err := FetchDoc(ctx, client, url)
		if err != nil {
			return nil, err
		}
		out = append(out, parseAnthologyCalendar(doc, cur.Year(), int(cur.Month()), theater)...)
		cur = cur.AddDate(0, 1, 0)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes found on calendar")
	}
	out = dedupeShowtimes(out)
	EnrichAnthologyFilmMeta(ctx, client, out, start, end)
	return out, nil
}

func parseAnthologyCalendar(doc *goquery.Document, year, month int, theater model.Theater) []model.Showtime {
	var out []model.Showtime
	doc.Find("td.calendar_day").Each(func(_ int, cell *goquery.Selection) {
		dayStr := strings.TrimSpace(cell.Find("span.day").First().Text())
		if dayStr == "" {
			return
		}
		day, err := strconv.Atoi(dayStr)
		if err != nil {
			return
		}
		dateStr := time.Date(year, time.Month(month), day, 0, 0, 0, 0, NYC()).Format("2006-01-02")
		if !InWindow(dateStr) {
			return
		}
		cell.Find("li.calendar_event").Each(func(_ int, ev *goquery.Selection) {
			text := strings.TrimSpace(ev.Text())
			lines := splitLines(text)
			if len(lines) == 0 {
				return
			}
			m := afaTimeRe.FindStringSubmatch(lines[0])
			if m == nil {
				return
			}
			title := strings.TrimSpace(ev.Find("a").First().Text())
			if title == "" && len(lines) > 1 {
				title = lines[1]
			}
			if title == "" {
				return
			}
			displayTitle := DisplayTitle(title)
			out = append(out, model.Showtime{
				TheaterID:   theater.ID,
				TheaterName: theater.Name,
				Title:       displayTitle,
				Date:        dateStr,
				Time:        format24(m[1], m[2], m[3]),
			})
		})
	})
	return out
}
