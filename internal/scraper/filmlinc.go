package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"movie-showtimes/internal/model"
)

type FilmlincParser struct{}

// filmlincAPIURL is the JSON feed behind filmlinc.org/now-playing. The page
// itself now sits behind Cloudflare, so scraping its HTML is unreliable, but
// this feed stays reachable and is what the page renders anyway.
const filmlincAPIURL = "https://api.filmlinc.org/showtimes"

// filmlincVenues are the spaces FLC programs itself. The feed also carries
// partner-venue NYFF screenings (BAM, MOMI, Alamo Staten Island) and passes;
// those would duplicate other theaters' calendars or aren't Films at all.
var filmlincVenues = map[string]struct{}{
	"Alice Tully Hall":        {},
	"Walter Reade Theater":    {},
	"Francesca Beale Theater": {},
	"Howard Gilman Theater":   {},
	"Amphitheater":            {},
}

type filmlincResponse struct {
	Films []filmlincFilm `json:"films"`
}

type filmlincFilm struct {
	Title     string             `json:"title"`
	Slug      string             `json:"slug"`
	Showtimes []filmlincShowtime `json:"showtimes"`
}

type filmlincShowtime struct {
	Date  string `json:"date"`
	Time  string `json:"time"`
	Venue string `json:"venue"`
}

func (FilmlincParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, filmlincAPIURL, nil)
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
		return nil, fmt.Errorf("HTTP %d from FLC showtimes API", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload filmlincResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := parseFilmlincPayload(payload, theater)
	if len(out) == 0 {
		return nil, fmt.Errorf("no showtimes from FLC showtimes API")
	}
	return dedupeShowtimes(out), nil
}

func parseFilmlincPayload(payload filmlincResponse, theater model.Theater) []model.Showtime {
	var out []model.Showtime
	for _, film := range payload.Films {
		title := DisplayTitle(film.Title)
		if title == "" {
			continue
		}
		filmURL := ""
		if film.Slug != "" {
			filmURL = AbsoluteURL("/films/"+url.PathEscape(film.Slug)+"/", "https://www.filmlinc.org")
		}
		for _, st := range film.Showtimes {
			if _, ok := filmlincVenues[st.Venue]; !ok {
				continue
			}
			if !InWindow(st.Date) {
				continue
			}
			t := NormalizeTime(st.Time)
			if !time24Re.MatchString(t) {
				continue
			}
			out = append(out, model.Showtime{
				TheaterID:   theater.ID,
				TheaterName: theater.Name,
				Title:       title,
				FilmURL:     filmURL,
				Date:        st.Date,
				Time:        t,
			})
		}
	}
	return out
}
