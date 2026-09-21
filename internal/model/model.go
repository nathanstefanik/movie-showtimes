package model

import "time"

type Theater struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Parser string `json:"parser"`
}

type Showtime struct {
	TheaterID   string `json:"theater_id"`
	TheaterName string `json:"theater_name"`
	Title       string `json:"title"`
	Director    string `json:"director,omitempty"`
	Year        string `json:"year,omitempty"`
	Overview    string `json:"overview,omitempty"`
	FilmURL     string `json:"film_url,omitempty"`
	Date        string `json:"date"`
	Time        string `json:"time"`
}

type ShowtimeCache struct {
	RefreshedAt time.Time         `json:"refreshed_at"`
	Showtimes   []Showtime        `json:"showtimes"`
	Errors      map[string]string `json:"errors"`
}

type TheaterStatus struct {
	Theater Theater `json:"theater"`
	Status  string  `json:"status"`
	Error   string  `json:"error,omitempty"`
}

type ShowtimeChip struct {
	Time        string `json:"time"`
	TheaterID   string `json:"theater_id"`
	TheaterName string `json:"theater_name"`
	FilmURL     string `json:"film_url,omitempty"`
}

type FilmTMDB struct {
	PosterURL   string `json:"poster_url,omitempty"`
	Overview    string `json:"overview,omitempty"`
	Director    string `json:"director,omitempty"`
	ReleaseYear string `json:"release_year,omitempty"`
}

// TMDBEntry is FilmTMDB plus FetchedAt, the on-disk cache row.
type TMDBEntry struct {
	PosterURL   string    `json:"poster_url,omitempty"`
	Overview    string    `json:"overview,omitempty"`
	Director    string    `json:"director,omitempty"`
	ReleaseYear string    `json:"release_year,omitempty"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
}

type TMDBCache struct {
	Entries map[string]TMDBEntry `json:"entries"`
}

type FilmDayEntry struct {
	Title            string         `json:"title"`
	NormalizedTitle  string         `json:"normalized_title"`
	TMDBURL          string         `json:"tmdb_url"`
	PosterURL        string         `json:"poster_url,omitempty"`
	Overview         string         `json:"overview,omitempty"`
	Director         string         `json:"director,omitempty"`
	ReleaseYear      string         `json:"release_year,omitempty"`
	Showtimes        []ShowtimeChip `json:"showtimes"`
	LetterboxdStatus string         `json:"letterboxd_status,omitempty"`
}

type DayGrid struct {
	Date  string         `json:"date"`
	Label string         `json:"label"`
	Films []FilmDayEntry `json:"films"`
}

type APIPayload struct {
	RefreshedAt          *time.Time        `json:"refreshed_at"`
	Showtimes            []Showtime        `json:"showtimes"`
	Errors               map[string]string `json:"errors"`
	TheaterStatus        []TheaterStatus   `json:"theater_status"`
	Grid                 []DayGrid         `json:"grid"`
	LetterboxdConfigured bool              `json:"letterboxd_configured"`
	TMDBConfigured       bool              `json:"tmdb_configured"`
}
