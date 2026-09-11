package scraper

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"movie-showtimes/internal/model"
)

func TestParseMetrographCardMeta(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="item film-thumbnail">
  <h4><a href="/film/?vista_film_id=1" class="title">Uncut Gems</a></h4>
  <div class="film-metadata">Josh  Safdie, Benny  Safdie / 2019 / 134min / 35mm</div>
</div>`))
	if err != nil {
		t.Fatal(err)
	}
	meta := parseMetrographCardMeta(doc.Find("div.item.film-thumbnail").First())
	if meta.Director != "Josh  Safdie, Benny  Safdie" || meta.Year != "2019" {
		t.Fatalf("meta = %+v, want director/year from the card", meta)
	}
}

func TestParseMetrographCardMetaNoDirector(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="item film-thumbnail">
  <h4><a href="/film/?vista_film_id=2" class="title">Some Program</a></h4>
  <div class="film-metadata">/ 2025 / 360min</div>
</div>`))
	if err != nil {
		t.Fatal(err)
	}
	meta := parseMetrographCardMeta(doc.Find("div.item.film-thumbnail").First())
	if meta.Director != "" || meta.Year != "2025" {
		t.Fatalf("meta = %+v, want empty director and year 2025", meta)
	}
}

func TestParseMetrographCalendarUsesCardMeta(t *testing.T) {
	today := time.Now().In(NYC()).Format("2006-01-02")
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="calendar-list-day movies-grid" id="calendar-list-day-` + today + `">
  <div class="item film-thumbnail">
    <h4><a href="/film/?vista_film_id=1" class="title">Troy</a></h4>
    <div class="film-metadata">Wolfgang Petersen / 2004 / 163min / 35mm</div>
    <div class="showtimes"> <a href="https://t.metrograph.com/x" title="Buy Tickets">3:20pm</a></div>
  </div>
  <div class="item film-thumbnail">
    <h4><a href="/film/?vista_film_id=2" class="title">Some Program</a></h4>
    <div class="film-metadata">/ 2025 / 360min</div>
    <div class="showtimes"> <a href="https://t.metrograph.com/y" title="Buy Tickets">5:00pm</a></div>
  </div>
</div>`))
	if err != nil {
		t.Fatal(err)
	}
	filmURLs := map[string]string{}
	shows := parseMetrographCalendar(doc, model.Theater{ID: "metrograph", Name: "Metrograph"}, filmURLs)
	if len(shows) != 2 {
		t.Fatalf("got %d showtimes, want 2: %+v", len(shows), shows)
	}
	if shows[0].Title != "Troy" || shows[0].Director != "Wolfgang Petersen" || shows[0].Year != "2004" || shows[0].Time != "15:20" {
		t.Errorf("Troy row = %+v", shows[0])
	}
	if shows[1].Director != "" || shows[1].Year != "2025" {
		t.Errorf("Program row = %+v, want no director/year from card", shows[1])
	}

	missing := metrographMissingMetaURLs(shows, filmURLs)
	if len(missing) != 1 {
		t.Fatalf("missing meta URLs = %v, want only the program", missing)
	}
	if _, ok := missing[NormalizeTitle("Some Program")]; !ok {
		t.Errorf("missing meta URLs = %v, want Some Program", missing)
	}
}
