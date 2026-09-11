package tmdb

import (
	"testing"
	"time"

	"movie-showtimes/internal/model"
)

func TestMergeTMDBEntryKeepsCachedFieldsOnEmptyResult(t *testing.T) {
	entry := model.TMDBEntry{
		PosterURL:   "https://image.tmdb.org/t/p/w185/old.jpg",
		Overview:    "old overview",
		Director:    "Old Director",
		ReleaseYear: "1979",
		FetchedAt:   time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	// A details call that failed yields a result with only the poster filled in.
	meta := &movieMeta{PosterURL: "https://image.tmdb.org/t/p/w185/new.jpg"}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	got := mergeTMDBEntry(entry, meta, now)

	if got.PosterURL != meta.PosterURL {
		t.Errorf("PosterURL = %q, want refreshed %q", got.PosterURL, meta.PosterURL)
	}
	if got.Overview != entry.Overview {
		t.Errorf("Overview = %q, want cached %q", got.Overview, entry.Overview)
	}
	if got.Director != entry.Director {
		t.Errorf("Director = %q, want cached %q", got.Director, entry.Director)
	}
	if got.ReleaseYear != entry.ReleaseYear {
		t.Errorf("ReleaseYear = %q, want cached %q", got.ReleaseYear, entry.ReleaseYear)
	}
	if !got.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, now)
	}
}
