package scraper

import "testing"

func TestBAMHasGenre(t *testing.T) {
	cases := []struct {
		genres string
		want   bool
	}{
		{"Film", true},
		{"Kids,Film", true},
		{"Film,Kids", true},
		{"film", true},
		{"Theater", false},
		{"Education", false},
		{"", false},
	}
	for _, c := range cases {
		if got := bamHasGenre(c.genres, "Film"); got != c.want {
			t.Errorf("bamHasGenre(%q) = %v, want %v", c.genres, got, c.want)
		}
	}
}
