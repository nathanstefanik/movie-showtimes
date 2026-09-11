package scraper

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseAnthologyListMeta(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="film-showing clearfix">
  <div class="showing-details">
    <a name="showing-1"> 4:15 PM</a>
    <br/>
    <span class="film-title">IN VANDA'S ROOM</span><br />
    by Pedro Costa<br />
    In Portuguese with English subtitles, 2000, 171 min, DCP<br/>
    <div class="film-notes" style="display:block">
      <p>Preceded by: SPLITTING 1974, 11 min, silent</p>
    </div>
  </div>
</div>
<div class="film-showing clearfix">
  <div class="showing-details">
    <span class="film-title">WELFARE</span><br />
    by Frederick Wiseman<br />
    1975, 167 min, 16mm-to-DCP<br/>
  </div>
</div>
<div class="film-showing clearfix">
  <div class="showing-details">
    <span class="film-title">EC: VAMPYR</span><br />
    by Carl Th. Dreyer<br />
    In Danish with no subtitles; English synopsis available, 1931-32, 70 min, 35mm<br/>
  </div>
</div>
<div class="film-showing clearfix">
  <div class="showing-details">
    <span class="film-title">THE GLORIA OF YOUR IMAGINATION</span><br />
    by Jennifer Reeves<br />
    2024/25, 98 min, 16mm + DCP<br/>
  </div>
</div>`))
	if err != nil {
		t.Fatal(err)
	}
	meta := parseAnthologyListMeta(doc)

	vanda, ok := meta[NormalizeTitle("IN VANDA'S ROOM")]
	if !ok {
		t.Fatalf("no meta for IN VANDA'S ROOM in %v", meta)
	}
	if vanda.Director != "Pedro Costa" || vanda.Year != "2000" {
		t.Errorf("IN VANDA'S ROOM meta = %+v, want Pedro Costa / 2000 (not the notes' 1974)", vanda)
	}

	welfare, ok := meta[NormalizeTitle("WELFARE")]
	if !ok {
		t.Fatalf("no meta for WELFARE in %v", meta)
	}
	if welfare.Director != "Frederick Wiseman" || welfare.Year != "1975" {
		t.Errorf("WELFARE meta = %+v, want Frederick Wiseman / 1975", welfare)
	}

	vampyr, ok := meta[NormalizeTitle("EC: VAMPYR")]
	if !ok {
		t.Fatalf("no meta for EC: VAMPYR in %v", meta)
	}
	if vampyr.Director != "Carl Th. Dreyer" || vampyr.Year != "1931" {
		t.Errorf("EC: VAMPYR meta = %+v, want Carl Th. Dreyer / 1931 (start of the 1931-32 range)", vampyr)
	}

	gloria, ok := meta[NormalizeTitle("THE GLORIA OF YOUR IMAGINATION")]
	if !ok {
		t.Fatalf("no meta for THE GLORIA OF YOUR IMAGINATION in %v", meta)
	}
	if gloria.Director != "Jennifer Reeves" || gloria.Year != "2024" {
		t.Errorf("GLORIA meta = %+v, want Jennifer Reeves / 2024 (start of the 2024/25 range)", gloria)
	}
}
