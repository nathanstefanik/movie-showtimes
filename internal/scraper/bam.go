package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"movie-showtimes/internal/model"
)

type BAMParser struct{}

type bamCalendarEvent struct {
	Performances []string `json:"performances"`
	Genres       string   `json:"genres"`
	Name         string   `json:"name"`
	MoreLink     string   `json:"moreLink"`
}

func (BAMParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	start, end := Window()
	url := fmt.Sprintf(
		"https://www.bam.org/api/BAMApi/GetCalendarEventsByDayWithOnGoing?start=%s&end=%s",
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from BAM calendar API", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var events []bamCalendarEvent
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, err
	}
	var out []model.Showtime
	filmURLs := map[string]string{}
	for _, ev := range events {
		if !bamHasGenre(ev.Genres, "Film") {
			continue
		}
		title := DisplayTitle(ev.Name)
		if title == "" {
			continue
		}
		if ev.MoreLink != "" {
			filmURLs[NormalizeTitle(title)] = AbsoluteURL(ev.MoreLink, "https://www.bam.org")
		}
		for _, perf := range ev.Performances {
			t, err := time.Parse(time.RFC3339, perf)
			if err != nil {
				continue
			}
			t = t.In(NYC())
			dateStr := t.Format("2006-01-02")
			if !InWindow(dateStr) {
				continue
			}
			out = append(out, model.Showtime{
				TheaterID:   theater.ID,
				TheaterName: theater.Name,
				Title:       title,
				Date:        dateStr,
				Time:        t.Format("15:04"),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no film showtimes from calendar API")
	}
	out = dedupeShowtimes(out)
	ApplyFilmURLs(out, filmURLs)
	EnrichFilmMetaFromURLs(ctx, client, out, filmURLs, ParseBAMFilmMeta)
	return out, nil
}

// bamHasGenre checks a comma-separated genre label ("Kids,Film") for a genre.
func bamHasGenre(genres, want string) bool {
	for _, g := range strings.Split(genres, ",") {
		if strings.EqualFold(strings.TrimSpace(g), want) {
			return true
		}
	}
	return false
}
