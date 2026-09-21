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

type RoxyParser struct{}

var roxyDateLineRe = regexp.MustCompile(`(?i)^(\d{2})\.(\d{2})\.(\d{4})\s*\|\s*(\d{1,2}:\d{2}\s*(?:AM|PM))`)

func (RoxyParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	doc, err := FetchDoc(ctx, client, "https://www.roxycinemanewyork.com/")
	if err != nil {
		return nil, err
	}
	filmURLs := map[string]string{}
	out := parseRoxyCards(doc, theater, filmURLs)
	if len(out) < 5 {
		if extra, err := fetchRoxyNowShowing(ctx, client, theater); err == nil {
			out = dedupeShowtimes(append(out, extra...))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes found on homepage")
	}
	out = dedupeShowtimes(out)
	ApplyFilmURLs(out, filmURLs)
	EnrichFilmMetaFromURLs(ctx, client, out, filmURLs, ParseRoxyFilmMeta)
	return out, nil
}

func parseRoxyCards(doc *goquery.Document, theater model.Theater, filmURLs map[string]string) []model.Showtime {
	var out []model.Showtime
	doc.Find("div.screening__card").Each(func(_ int, card *goquery.Selection) {
		titleEl := card.Find("h3.screening__title a, h3.screening__title").First()
		title := strings.TrimSpace(titleEl.Text())
		if title == "" {
			return
		}
		displayTitle := DisplayTitle(title)
		if a := card.Find("h3.screening__title a").First(); a.Length() > 0 {
			if href, ok := a.Attr("href"); ok {
				filmURLs[NormalizeTitle(displayTitle)] = AbsoluteURL(href, "https://www.roxycinemanewyork.com")
			}
		}
		if dt, ok := card.Attr("data-datetime"); ok && dt != "" {
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", dt); err == nil {
				t = t.In(NYC())
				dateStr := t.Format("2006-01-02")
				if InWindow(dateStr) {
					out = append(out, model.Showtime{
						TheaterID:   theater.ID,
						TheaterName: theater.Name,
						Title:       displayTitle,
						Date:        dateStr,
						Time:        t.Format("15:04"),
					})
				}
				return
			}
		}
		dateText := strings.TrimSpace(card.Find("p.screening__date").First().Text())
		if dateText == "" {
			return
		}
		if s := parseRoxyDateLine(title, dateText, theater); s != nil {
			out = append(out, *s)
		}
	})
	return out
}

func parseRoxyDateLine(title, line string, theater model.Theater) *model.Showtime {
	m := roxyDateLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	dateStr := fmt.Sprintf("%s-%s-%s", m[3], m[2], m[1])
	if !InWindow(dateStr) {
		return nil
	}
	return &model.Showtime{
		TheaterID:   theater.ID,
		TheaterName: theater.Name,
		Title:       DisplayTitle(title),
		Date:        dateStr,
		Time:        NormalizeTime(m[4]),
	}
}

func fetchRoxyNowShowing(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	doc, err := FetchDoc(ctx, client, "https://www.roxycinemanewyork.com/now-showing/")
	if err != nil {
		return nil, err
	}
	var out []model.Showtime
	doc.Find("a[href*='/screenings/']").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if href == "" || strings.Contains(href, "/events/") {
			return
		}
		title := strings.TrimSpace(a.Text())
		if title == "" {
			return
		}
		filmURL := AbsoluteURL(href, "https://www.roxycinemanewyork.com")
		page, err := FetchDoc(ctx, client, filmURL)
		if err != nil {
			return
		}
		meta := ParseRoxyFilmMeta(page)
		for _, s := range parseRoxyScreeningPage(page, title, theater, meta) {
			s.FilmURL = filmURL
			out = append(out, s)
		}
	})
	return out, nil
}

func parseRoxyScreeningPage(doc *goquery.Document, title string, theater model.Theater, meta FilmMeta) []model.Showtime {
	var out []model.Showtime
	displayTitle := DisplayTitle(title)
	doc.Find("p.screening__date, .upcoming-shows li, .screening__show").Each(func(_ int, s *goquery.Selection) {
		line := strings.TrimSpace(s.Text())
		if s := parseRoxyDateLine(displayTitle, line, theater); s != nil {
			s.Director = meta.Director
			s.Year = meta.Year
			s.Overview = meta.Overview
			out = append(out, *s)
		}
	})
	body := doc.Text()
	for _, line := range splitLines(body) {
		if s := parseRoxyDateLine(displayTitle, line, theater); s != nil {
			s.Director = meta.Director
			s.Year = meta.Year
			s.Overview = meta.Overview
			out = append(out, *s)
		}
	}
	return out
}
