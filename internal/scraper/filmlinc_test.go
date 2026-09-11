package scraper

import (
	"testing"
	"time"

	"movie-showtimes/internal/model"
)

func TestParseFilmlincPayload(t *testing.T) {
	now := time.Now().In(NYC())
	today := now.Format("2006-01-02")
	soon := now.AddDate(0, 0, 3).Format("2006-01-02")
	far := now.AddDate(0, 0, 30).Format("2006-01-02")

	payload := filmlincResponse{
		Films: []filmlincFilm{
			{
				Title: "Late Fame [New 4K Restoration]",
				Slug:  "late-fame",
				Showtimes: []filmlincShowtime{
					{Date: soon, Time: "8:30 PM", Venue: "Howard Gilman Theater"},
					{Date: soon, Time: "1:15 PM", Venue: "Francesca Beale Theater"},
					{Date: far, Time: "9:00 PM", Venue: "Howard Gilman Theater"},
					{Date: soon, Time: "6:00 PM", Venue: "BAM"},
					{Date: soon, Time: "11:00 PM", Venue: "Pass Venue"},
					{Date: soon, Time: "TBA", Venue: "Walter Reade Theater"},
				},
			},
			{
				Title: "Midnight Movie",
				Slug:  "midnight-movie",
				Showtimes: []filmlincShowtime{
					{Date: today, Time: "12:00 AM", Venue: "Walter Reade Theater"},
				},
			},
		},
	}

	got := parseFilmlincPayload(payload, model.Theater{ID: "filmlinc", Name: "Film at Lincoln Center"})
	want := []model.Showtime{
		{TheaterID: "filmlinc", TheaterName: "Film at Lincoln Center", Title: "Late Fame", FilmURL: "https://www.filmlinc.org/films/late-fame/", Date: soon, Time: "20:30"},
		{TheaterID: "filmlinc", TheaterName: "Film at Lincoln Center", Title: "Late Fame", FilmURL: "https://www.filmlinc.org/films/late-fame/", Date: soon, Time: "13:15"},
		{TheaterID: "filmlinc", TheaterName: "Film at Lincoln Center", Title: "Midnight Movie", FilmURL: "https://www.filmlinc.org/films/midnight-movie/", Date: today, Time: "00:00"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d showtimes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("showtime %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
