package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"movie-showtimes/internal/model"
)

type ParisParser struct{}

// Public browsing credentials embedded in paristheaternyc.com's client bundle.
const (
	parisAuthURL  = "https://auth.moviexchange.com/connect/token"
	parisAPIBase  = "https://digital-api.paristheaternyc.com/ocapi/v1"
	parisSiteID   = "2001"
	parisClientID = "webhost-browsing-parisnyc"
	parisUsername = "webhost-browsing-parisnyc"
	parisPassword = "HzaJe65EAPNto7sR5"
	parisSiteURL  = "https://www.paristheaternyc.com"
	parisCMSURL   = "https://cms.ntflxthtrs.com/api/films"
)

// ponytail: Next.js embeds Strapi film JSON in the homepage HTML. If they
// change the payload shape, fall through to the CMS API (parisCMSFilmSlugs).
var parisFilmSlugRe = regexp.MustCompile(`FilmName\\":\\"([^\\]+)\\".*?Slug\\":\\"([^\\]+)\\".*?VistaIDOverride\\":\\"(HO[0-9]+)\\"`)

type parisLocalizedText struct {
	Text string `json:"text"`
}

type parisPersonName struct {
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

type parisCastMember struct {
	ID   string          `json:"id"`
	Name parisPersonName `json:"name"`
}

type parisDirectorRef struct {
	CastAndCrewMemberID string `json:"castAndCrewMemberId"`
}

type parisFilm struct {
	ID          string             `json:"id"`
	Title       parisLocalizedText `json:"title"`
	Synopsis    parisLocalizedText `json:"synopsis"`
	ReleaseDate string             `json:"releaseDate"`
	Directors   []parisDirectorRef `json:"directors"`
}

type parisShowtime struct {
	ID       string `json:"id"`
	FilmID   string `json:"filmId"`
	Schedule struct {
		BusinessDate string `json:"businessDate"`
		StartsAt     string `json:"startsAt"`
	} `json:"schedule"`
}

type parisDayPayload struct {
	BusinessDate string          `json:"businessDate"`
	Showtimes    []parisShowtime `json:"showtimes"`
	RelatedData  struct {
		Films       []parisFilm       `json:"films"`
		CastAndCrew []parisCastMember `json:"castAndCrew"`
	} `json:"relatedData"`
}

func (ParisParser) Fetch(ctx context.Context, client *http.Client, theater model.Theater) ([]model.Showtime, error) {
	var (
		token   string
		slugs   map[string]string
		authErr error
	)
	var setup sync.WaitGroup
	setup.Add(2)
	go func() {
		defer setup.Done()
		token, authErr = parisAccessToken(ctx, client)
	}()
	go func() {
		defer setup.Done()
		slugs = parisFilmSlugMap(ctx, client)
	}()
	setup.Wait()
	if authErr != nil {
		return nil, authErr
	}
	if slugs == nil {
		slugs = map[string]string{}
	}

	dates := WeekGridDates()
	var (
		mu  sync.Mutex
		out []model.Showtime
	)
	var wg sync.WaitGroup
	for _, day := range dates {
		wg.Add(1)
		go func(day time.Time) {
			defer wg.Done()
			dayShows, err := parisShowtimesForDate(ctx, client, token, day, theater, slugs)
			if err != nil {
				// ponytail: one day's API blip shouldn't fail the whole theater.
				return
			}
			mu.Lock()
			out = append(out, dayShows...)
			mu.Unlock()
		}(day)
	}
	wg.Wait()

	out = dedupeShowtimes(out)
	if len(out) > 0 {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("Paris Theater API: %w", err)
	}
	return nil, fmt.Errorf("no showtimes from Paris Theater API")
}

func parisFilmSlugMap(ctx context.Context, client *http.Client) map[string]string {
	slugs, err := parisFilmSlugs(ctx, client)
	if err == nil && len(slugs) > 0 {
		return slugs
	}
	slugs, err = parisCMSFilmSlugs(ctx, client)
	if err != nil {
		return map[string]string{}
	}
	return slugs
}

func parisFilmSlugs(ctx context.Context, client *http.Client) (map[string]string, error) {
	doc, err := FetchDoc(ctx, client, parisSiteURL+"/")
	if err != nil {
		return nil, err
	}
	return parseParisFilmSlugs(doc.Text()), nil
}

func parseParisFilmSlugs(html string) map[string]string {
	out := map[string]string{}
	for _, m := range parisFilmSlugRe.FindAllStringSubmatch(html, -1) {
		out[m[3]] = m[2]
	}
	return out
}

type parisCMSFilm struct {
	Attributes struct {
		Slug            string `json:"Slug"`
		VistaID         string `json:"VistaID"`
		VistaIDOverride string `json:"VistaIDOverride"`
	} `json:"attributes"`
}

type parisCMSFilmsResponse struct {
	Data []parisCMSFilm `json:"data"`
	Meta struct {
		Pagination struct {
			Page      int `json:"page"`
			PageCount int `json:"pageCount"`
		} `json:"pagination"`
	} `json:"meta"`
}

func parisCMSFilmSlugs(ctx context.Context, client *http.Client) (map[string]string, error) {
	out := map[string]string{}
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("pagination[page]", fmt.Sprintf("%d", page))
		q.Set("pagination[pageSize]", "100")
		q.Set("filters[Association][$contains]", "Paris")
		q.Set("fields[0]", "Slug")
		q.Set("fields[1]", "VistaID")
		q.Set("fields[2]", "VistaIDOverride")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parisCMSURL+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := func() ([]byte, error) {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("HTTP %d from Paris CMS", resp.StatusCode)
			}
			return io.ReadAll(resp.Body)
		}()
		if err != nil {
			return nil, err
		}
		var payload parisCMSFilmsResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		for _, film := range payload.Data {
			vistaID := film.Attributes.VistaIDOverride
			if vistaID == "" {
				vistaID = film.Attributes.VistaID
			}
			if vistaID != "" && film.Attributes.Slug != "" {
				out[vistaID] = film.Attributes.Slug
			}
		}
		if page >= payload.Meta.Pagination.PageCount {
			break
		}
	}
	return out, nil
}

func parisAccessToken(ctx context.Context, client *http.Client) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", parisUsername)
	form.Set("password", parisPassword)
	form.Set("client_id", parisClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parisAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from Paris auth", resp.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("empty access token from Paris auth")
	}
	return payload.AccessToken, nil
}

func parisShowtimesForDate(ctx context.Context, client *http.Client, token string, day time.Time, theater model.Theater, slugs map[string]string) ([]model.Showtime, error) {
	dateStr := day.Format("2006-01-02")
	apiURL := fmt.Sprintf("%s/showtimes/by-business-date/%s?siteIds=%s", parisAPIBase, dateStr, parisSiteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from Paris showtimes for %s", resp.StatusCode, dateStr)
	}
	var payload parisDayPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return parseParisDay(payload, theater, slugs), nil
}

func parseParisDay(payload parisDayPayload, theater model.Theater, slugs map[string]string) []model.Showtime {
	films := map[string]parisFilm{}
	for _, f := range payload.RelatedData.Films {
		films[f.ID] = f
	}
	crew := map[string]string{}
	for _, c := range payload.RelatedData.CastAndCrew {
		name := strings.TrimSpace(c.Name.GivenName + " " + c.Name.FamilyName)
		if name != "" {
			crew[c.ID] = name
		}
	}
	var out []model.Showtime
	for _, st := range payload.Showtimes {
		film, ok := films[st.FilmID]
		if !ok {
			continue
		}
		title := DisplayTitle(strings.TrimSpace(film.Title.Text))
		if title == "" {
			continue
		}
		startsAt := st.Schedule.StartsAt
		if startsAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, startsAt)
		if err != nil {
			continue
		}
		t = t.In(NYC())
		dateStr := t.Format("2006-01-02")
		if !InWindow(dateStr) {
			continue
		}
		director := ""
		if len(film.Directors) > 0 {
			director = crew[film.Directors[0].CastAndCrewMemberID]
		}
		year := ""
		if len(film.ReleaseDate) >= 4 {
			year = film.ReleaseDate[:4]
		}
		overview := strings.TrimSpace(film.Synopsis.Text)
		if len(overview) > 500 {
			overview = overview[:500]
		}
		filmURL := ""
		if slug := slugs[st.FilmID]; slug != "" {
			filmURL = AbsoluteURL("/film/"+slug, parisSiteURL)
		}
		out = append(out, model.Showtime{
			TheaterID:   theater.ID,
			TheaterName: theater.Name,
			Title:       title,
			Director:    director,
			Year:        year,
			Overview:    overview,
			FilmURL:     filmURL,
			Date:        dateStr,
			Time:        t.Format("15:04"),
		})
	}
	return out
}
