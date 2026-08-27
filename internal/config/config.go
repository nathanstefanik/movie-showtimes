package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"movie-showtimes/internal/fsutil"
	"movie-showtimes/internal/model"
)

const theatersFile = "data/theaters.json"

func LoadTheaters() ([]model.Theater, error) {
	data, err := os.ReadFile(theatersFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var theaters []model.Theater
	if err := json.Unmarshal(data, &theaters); err != nil {
		return nil, err
	}
	return theaters, nil
}

func SaveTheaters(theaters []model.Theater) error {
	data, err := json.MarshalIndent(theaters, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(theatersFile, append(data, '\n'), 0o644)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string, existing []model.Theater) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "theater"
	}
	base := s
	n := 1
	ids := map[string]struct{}{}
	for _, t := range existing {
		ids[t.ID] = struct{}{}
	}
	for {
		if _, ok := ids[s]; !ok {
			return s
		}
		n++
		s = fmt.Sprintf("%s-%d", base, n)
	}
}
