package scraper

import (
	"testing"
	"time"

	"movie-showtimes/internal/model"
)

func TestParseParisFilmSlugs(t *testing.T) {
	html := `FilmName\":\"Lawrence of Arabia\",\"Slug\":\"lawrence-of-arabia-paris\",\"VistaIDOverride\":\"HO00000041\"`
	got := parseParisFilmSlugs(html)
	if got["HO00000041"] != "lawrence-of-arabia-paris" {
		t.Fatalf("slug map = %v", got)
	}
}

func TestParseParisDay(t *testing.T) {
	// InWindow uses today; a hardcoded date ages out of the 14-day window.
	day := time.Now().In(NYC()).AddDate(0, 0, 1)
	startsAt := time.Date(day.Year(), day.Month(), day.Day(), 11, 0, 0, 0, NYC())
	payload := parisDayPayload{
		Showtimes: []parisShowtime{{
			ID:     "2001-2934",
			FilmID: "HO00000041",
			Schedule: struct {
				BusinessDate string `json:"businessDate"`
				StartsAt     string `json:"startsAt"`
			}{
				StartsAt: startsAt.Format(time.RFC3339),
			},
		}},
	}
	payload.RelatedData.Films = []parisFilm{{
		ID:          "HO00000041",
		Title:       parisLocalizedText{Text: "Lawrence of Arabia"},
		Synopsis:    parisLocalizedText{Text: "Epic."},
		ReleaseDate: "1962-12-16",
		Directors:   []parisDirectorRef{{CastAndCrewMemberID: "HO00000248"}},
	}}
	payload.RelatedData.CastAndCrew = []parisCastMember{{
		ID:   "HO00000248",
		Name: parisPersonName{GivenName: "David", FamilyName: "Lean"},
	}}

	slugs := map[string]string{"HO00000041": "lawrence-of-arabia-paris"}
	got := parseParisDay(payload, model.Theater{ID: "paris", Name: "Paris Theater"}, slugs)
	if len(got) != 1 {
		t.Fatalf("got %d showtimes, want 1: %+v", len(got), got)
	}
	want := model.Showtime{
		TheaterID:   "paris",
		TheaterName: "Paris Theater",
		Title:       "Lawrence of Arabia",
		Director:    "David Lean",
		Year:        "1962",
		Overview:    "Epic.",
		FilmURL:     "https://www.paristheaternyc.com/film/lawrence-of-arabia-paris",
		Date:        startsAt.Format("2006-01-02"),
		Time:        "11:00",
	}
	if got[0] != want {
		t.Fatalf("showtime = %+v, want %+v", got[0], want)
	}
}
