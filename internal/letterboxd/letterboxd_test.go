package letterboxd

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeLibrary(t *testing.T) *Library {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"watched.csv":   "Date,Name,Year,Letterboxd URI\n2024-01-01,Stalker,1979,http://x\n2024-01-02,Solaris,1972,http://y\n",
		"watchlist.csv": "Date,Name,Year,Letterboxd URI\n2024-01-01,Mirror,1975,http://z\n",
		"ratings.csv":   "Date,Name,Year,Letterboxd URI,Rating\n2024-01-01,Stalker,1979,http://x,4.5\n2024-01-02,Solaris,1972,http://y,1.5\n2024-01-03,Nostalghia,1983,http://w,\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Library{dir: dir}
}

func TestStatus(t *testing.T) {
	lb := writeLibrary(t)
	if !lb.Configured() {
		t.Fatal("library reported unconfigured")
	}
	// Nostalghia has a blank rating and appears in no other export, so it is
	// not part of the library at all.
	if got := lb.FilmCount(); got != 3 {
		t.Fatalf("film count %d, want 3", got)
	}

	cases := map[string]string{
		"Stalker":           "loved",     // 4.5 stars
		"Solaris":           "disliked",  // 1.5 stars
		"Mirror":            "watchlist", // watchlist only
		"Nostalghia (1983)": "",          // blank rating, in no other export
		"Sátántangó":        "",          // absent
	}
	for title, want := range cases {
		if got := lb.Status(title); got != want {
			t.Errorf("Status(%q) = %q, want %q", title, got, want)
		}
	}
}

// Status is called once per film while the grid is built and concurrently by
// separate requests; the library reloads its maps in place.
func TestStatusIsConcurrencySafe(t *testing.T) {
	lb := writeLibrary(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := lb.Status("Stalker"); got != "loved" {
					t.Errorf("Status = %q, want loved", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRemovingAnExportReloads(t *testing.T) {
	lb := writeLibrary(t)
	if got := lb.Status("Stalker"); got != "loved" {
		t.Fatalf("Status(Stalker) = %q, want loved", got)
	}

	// Skip the stat throttle so the reload check runs on the next call.
	lb.mu.Lock()
	lb.lastStat = time.Time{}
	lb.mu.Unlock()

	for _, name := range []string{"watched.csv", "ratings.csv"} {
		if err := os.Remove(filepath.Join(lb.dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	if got := lb.Status("Stalker"); got != "" {
		t.Errorf("Status(Stalker) = %q, want empty after its exports were removed", got)
	}
	if got := lb.Status("Mirror"); got != "watchlist" {
		t.Errorf("Status(Mirror) = %q, want watchlist", got)
	}
}

func TestMissingExportsAreNotAnError(t *testing.T) {
	lb := &Library{dir: t.TempDir()}
	if lb.Configured() {
		t.Fatal("empty directory reported as configured")
	}
	if got := lb.Status("Stalker"); got != "" {
		t.Fatalf("Status = %q, want empty", got)
	}
}
