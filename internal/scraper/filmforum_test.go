package scraper

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func filmForumDoc(t *testing.T, body string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseFilmForumFilmMetaNewRelease(t *testing.T) {
	doc := filmForumDoc(t, `
<div class="urgent"><p>DIRECTED BY KENT JONES<br />
STARRING WILLEM DAFOE</p></div>
<div class="copy">
  <p>Willem Dafoe is Ed Saxberger.</p>
  <strong>2025&nbsp; &nbsp;96 MIN.&nbsp; &nbsp;USA&nbsp; &nbsp;MAGNOLIA PICTURES</strong>
</div>`)
	meta := ParseFilmForumFilmMeta(doc)
	if meta.Director != "KENT JONES" || meta.Year != "2025" {
		t.Fatalf("meta = %+v, want KENT JONES / 2025", meta)
	}
}

func TestParseFilmForumFilmMetaRepertory(t *testing.T) {
	doc := filmForumDoc(t, `
<div class="urgent"><p>NEW 4K RESTORATION</p></div>
<div class="copy">
  <p><strong>Japan, 1964<br />
Directed by Masaki Kobayashi<br />
Approx. 183 min.</strong><br />Four ghost stories.</p>
</div>`)
	meta := ParseFilmForumFilmMeta(doc)
	if meta.Director != "Masaki Kobayashi" || meta.Year != "1964" {
		t.Fatalf("meta = %+v, want Masaki Kobayashi / 1964", meta)
	}
}

func TestParseFilmForumFilmMetaIgnoresSynopsisProse(t *testing.T) {
	doc := filmForumDoc(t, `
<div class="urgent"><p>Premiered at Film Forum on December 4, 1991</p></div>
<div class="copy">
  <p>A psychological horror story starring a dummy, written and directed by Sandor Stern, screenwriter of THE AMITYVILLE HORROR (1979).</p>
  <strong>1988 103 MIN. CANADA</strong>
</div>`)
	meta := ParseFilmForumFilmMeta(doc)
	if meta.Director != "" {
		t.Errorf("director = %q, want empty (prose is not a credit)", meta.Director)
	}
	if meta.Year != "1988" {
		t.Errorf("year = %q, want 1988", meta.Year)
	}
}
