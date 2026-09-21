package cache

import (
	"encoding/json"
	"os"
	"time"

	"movie-showtimes/internal/fsutil"
	"movie-showtimes/internal/model"
)

const (
	showtimesFile = "data/showtimes.json"
	tmdbFile      = "data/tmdb.json"
)

func LoadShowtimes() (*model.ShowtimeCache, error) {
	data, err := os.ReadFile(showtimesFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.ShowtimeCache{
				Errors:    map[string]string{},
				Showtimes: []model.Showtime{},
			}, nil
		}
		return nil, err
	}
	var c model.ShowtimeCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Errors == nil {
		c.Errors = map[string]string{}
	}
	if c.Showtimes == nil {
		c.Showtimes = []model.Showtime{}
	}
	return &c, nil
}

func SaveShowtimes(c *model.ShowtimeCache) error {
	if c.Errors == nil {
		c.Errors = map[string]string{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(showtimesFile, append(data, '\n'), 0o644)
}

func LoadTMDB() (*model.TMDBCache, error) {
	data, err := os.ReadFile(tmdbFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.TMDBCache{Entries: map[string]model.TMDBEntry{}}, nil
		}
		return nil, err
	}
	var c model.TMDBCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Entries == nil {
		c.Entries = map[string]model.TMDBEntry{}
	}
	return &c, nil
}

func SaveTMDB(c *model.TMDBCache) error {
	if c.Entries == nil {
		c.Entries = map[string]model.TMDBEntry{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(tmdbFile, append(data, '\n'), 0o644)
}

// FileStamp identifies a version of a cache file cheaply enough to check on
// every request.
type FileStamp struct {
	ModTime time.Time
	Size    int64
}

func TMDBStamp() FileStamp {
	info, err := os.Stat(tmdbFile)
	if err != nil {
		return FileStamp{}
	}
	return FileStamp{ModTime: info.ModTime(), Size: info.Size()}
}
