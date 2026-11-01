package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These tests use small hand-written HTML snippets shaped like the site's
// pages. If the site changes its markup, save a real page under testdata/
// and add a case here so the parser that broke is easy to find.

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"Sunny: Day?":            "Sunny- Day",
		"a/b\\c|d":               "a-b-c-d",
		"  lots \n\t of  space ": "lots of space",
		"ends with dots...":      "ends with dots",
		`<"quoted">`:             "quoted",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateName(t *testing.T) {
	if got := truncateName("héllo wörld", 6); got != "héllo" {
		t.Errorf("truncateName = %q, want %q", got, "héllo")
	}
	if got := truncateName("short", 80); got != "short" {
		t.Errorf("truncateName = %q, want %q", got, "short")
	}
}

func TestTitleCaseModelName(t *testing.T) {
	cases := map[string]string{
		"bob":        "Bob",
		"mary-jane/": "Mary Jane",
		"":           "Unknown",
	}
	for in, want := range cases {
		if got := titleCaseModelName(in); got != want {
			t.Errorf("titleCaseModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePageInfo(t *testing.T) {
	album := parsePageInfo("Bob Photo Album: Sunny Day | SuicideGirls")
	if album.ModelName != "Bob" || album.AlbumName != "Sunny Day" || album.IsCandid {
		t.Errorf("album title parsed as %+v", album)
	}

	candid := parsePageInfo("Hello there by Bob | SuicideGirls")
	if candid.ModelName != "Bob" || candid.PostName != "Hello there" || !candid.IsCandid {
		t.Errorf("candid title parsed as %+v", candid)
	}
}

func TestGetNextOffset(t *testing.T) {
	page := `<html><head><link rel="next" href="/girls/bob/photos/?offset=24&x=1"></head></html>`
	if got := getNextOffset(strings.NewReader(page)); got != 24 {
		t.Errorf("getNextOffset = %d, want 24", got)
	}
	if got := getNextOffset(strings.NewReader(`<html><head></head></html>`)); got != -1 {
		t.Errorf("getNextOffset on last page = %d, want -1", got)
	}
}

func TestCrawlAlbumsFiltersByModel(t *testing.T) {
	page := `
		<a href="/girls/bob/album/100/sunny-day/">mine</a>
		<a href="/girls/bob/album/100/sunny-day/">duplicate</a>
		<a href="/girls/alice/album/200/other/">someone else</a>
		<a href="https://example.com/girls/bob/album/300/">off-site</a>`
	got := crawlAlbums(strings.NewReader(page), "Bob")
	want := []string{"https://www.suicidegirls.com/girls/bob/album/100/sunny-day/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("crawlAlbums = %v, want %v", got, want)
	}
}

func TestCrawlGroupThreadKeepsImageOrder(t *testing.T) {
	page := `
		<div class="flex-wrapper" data-comment-id="111">
		  <a href="/members/carol/">carol</a>
		  <div class="comment-text" data-comment-id="111">look
		    <img data-original="https://cdn.example/3.jpg">
		    <img data-original="https://cdn.example/1.jpg">
		    <img data-original="https://cdn.example/2.jpg">
		  </div><time>now</time>
		</div>`
	buckets := crawlGroupThreadImageBuckets(strings.NewReader(page))
	if len(buckets) != 1 {
		t.Fatalf("got %d buckets, want 1", len(buckets))
	}
	b := buckets[0]
	if b.CommentID != "111" || b.Username != "carol" {
		t.Errorf("bucket = %+v", b)
	}
	want := []string{"https://cdn.example/3.jpg", "https://cdn.example/1.jpg", "https://cdn.example/2.jpg"}
	if !reflect.DeepEqual(b.Images, want) {
		t.Errorf("images = %v, want page order %v", b.Images, want)
	}
}

func TestGetAlbumDate(t *testing.T) {
	got, err := getAlbumDate(strings.NewReader(`<time>Mar 4, 2020</time>`))
	if err != nil || !got.Equal(time.Date(2020, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("full date = %v, %v", got, err)
	}

	// A yearless date that would be in the future belongs to last year.
	soon := time.Now().AddDate(0, 0, 2)
	got, err = getAlbumDate(strings.NewReader(`<time>` + soon.Format("Jan 2") + `</time>`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Year() != soon.Year()-1 || got.Month() != soon.Month() || got.Day() != soon.Day() {
		t.Errorf("yearless future date = %v, want %s of the previous year", got, soon.Format("Jan 2"))
	}
}

func TestRouteFor(t *testing.T) {
	cases := map[string]string{
		"https://www.suicidegirls.com/girls/bob/":                        routeProfile,
		"https://www.suicidegirls.com/members/bob?ref=x":                 routeProfile,
		"https://www.suicidegirls.com/girls/bob/album/123/sunny/":        routeAlbum,
		"https://www.suicidegirls.com/girls/bob/photos/view/candids/":    routePhotos,
		"https://www.suicidegirls.com/girls/bob/videos/":                 routeModelVideos,
		"https://www.suicidegirls.com/videos/55/some-video/":             routeVideo,
		"https://www.suicidegirls.com/girls/bob/blog/":                   routeModelBlog,
		"https://www.suicidegirls.com/members/bob/blog/77/post/":         routeBlogPost,
		"https://www.suicidegirls.com/groups/cats/thread/9/hello/":       routeGroupThread,
		"https://suicidegirls.com/girls/bob/":                            routeProfile,
		"https://www.suicidegirls.com/girls/bob/typo/":                   "",
		"https://www.suicidegirls.com/":                                  "",
		"https://example.com/girls/bob/":                                 "",
		"www.suicidegirls.com/girls/bob/":                                "",
		"https://www.suicidegirls.com.evil.example/girls/bob/album/1/x/": "",
	}
	for in, want := range cases {
		if got := routeFor(in); got != want {
			t.Errorf("routeFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProfileBase(t *testing.T) {
	base, model, ok := profileBase("https://www.suicidegirls.com/girls/bob/?x=1")
	if !ok || base != "https://www.suicidegirls.com/girls/bob" || model != "bob" {
		t.Errorf("profileBase = %q, %q, %v", base, model, ok)
	}
}

func TestFindExistingEntry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "1234 - other.jpg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "123 - half.jpg.part"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "555 - folder"), 0o755)

	if got := findExistingEntry(dir, "123 - ", false); got != "" {
		t.Errorf("123 matched %q; a longer ID or a .part file must not count", got)
	}
	if got := findExistingEntry(dir, "1234 - ", false); got != "1234 - other.jpg" {
		t.Errorf("1234 = %q", got)
	}
	if got := findExistingEntry(dir, "555 - ", true); got != "555 - folder" {
		t.Errorf("folder = %q", got)
	}
	if got := findExistingEntry(dir, "555 - ", false); got != "" {
		t.Errorf("folder matched as a file: %q", got)
	}
}

func TestIDAfter(t *testing.T) {
	cases := []struct {
		url, want string
		segments  []string
	}{
		{"https://www.suicidegirls.com/girls/bob/album/123/sunny-day/", "123", []string{"album", "blog"}},
		{"https://www.suicidegirls.com/members/bob/blog/77/post", "77", []string{"album", "blog"}},
		{"https://www.suicidegirls.com/videos/55/", "55", []string{"videos"}},
		{"https://www.suicidegirls.com/girls/bob/", "", []string{"album"}},
	}
	for _, c := range cases {
		if got := idAfter(c.url, c.segments...); got != c.want {
			t.Errorf("idAfter(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestKnownDownloaded(t *testing.T) {
	model := fmt.Sprintf("Test Model %d", time.Now().UnixNano())
	path := modelDBPath(model)
	defer os.Remove(path)

	// No history yet: nothing is known, and no empty database is created.
	if knownDownloaded(model, "123", "album") {
		t.Error("unknown model reported item 123 as downloaded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("knownDownloaded created %s", path)
	}

	db, err := getModelDB(model)
	if err != nil {
		t.Fatal(err)
	}
	markDownloaded(db, "candid", "123", "post")
	db.Close()

	if !knownDownloaded(model, "123", "album", "candid") {
		t.Error("recorded candid 123 not found")
	}
	if knownDownloaded(model, "123", "video") {
		t.Error("candid 123 matched as a video")
	}
	if knownDownloaded(model, "999", "album", "candid") {
		t.Error("unrecorded item 999 reported as downloaded")
	}
}

func TestPrintSkipCountsHiddenSkips(t *testing.T) {
	defer func(old bool, oldCount int) { showSkips, runStats.skipped = old, oldCount }(showSkips, runStats.skipped)
	showSkips = false
	runStats.skipped = 0
	printSkip("", "[skip] hidden")
	if runStats.skipped != 1 {
		t.Errorf("skipped = %d, want 1 even when skip lines are hidden", runStats.skipped)
	}
}

func TestImageExtFor(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":               ".jpg",
		"image/png":                ".png",
		"image/webp; charset=utf8": ".webp",
		"application/octet-stream": "",
		"":                         "",
	}
	for in, want := range cases {
		if got := imageExtFor(in); got != want {
			t.Errorf("imageExtFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithRetry(t *testing.T) {
	defer func(old time.Duration) { retryBaseWait = old }(retryBaseWait)
	retryBaseWait = time.Millisecond

	calls := 0
	err := withRetry("flaky", func() error {
		calls++
		if calls < 3 {
			return errors.New("connection reset")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("transient errors: err=%v calls=%d, want nil after 3 calls", err, calls)
	}

	calls = 0
	err = withRetry("404", func() error {
		calls++
		return permanent(errors.New("not found"))
	})
	if err == nil || calls != 1 {
		t.Errorf("permanent error: err=%v calls=%d, want an error after 1 call", err, calls)
	}
}

func TestGetAllLinks(t *testing.T) {
	t.Setenv("DOWNLOAD_DELAY_MS", "0")
	defer func(old []string) { failedItems = old }(failedItems)
	failedItems = nil

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("offset") {
		case "":
			w.Write([]byte(`<link rel="next" href="?offset=2"><a href="/a">a</a>`))
		case "2":
			w.Write([]byte(`<link rel="next" href="?offset=4"><a href="/b">b</a><a href="/a">a again</a>`))
		default:
			http.NotFound(w, r) // a broken later page
		}
	}))
	defer srv.Close()

	crawl := func(r io.Reader) []string {
		body, _ := io.ReadAll(r)
		var links []string
		for _, l := range []string{"/a", "/b"} {
			if strings.Contains(string(body), `href="`+l+`"`) {
				links = append(links, l)
			}
		}
		return links
	}

	got := getAllLinks(srv.URL+"/list/", crawl)
	if want := []string{"/a", "/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("links = %v, want %v (deduplicated, kept when a later page fails)", got, want)
	}
	if len(failedItems) != 1 {
		t.Errorf("failed pages recorded = %d, want 1", len(failedItems))
	}
}

func TestSaveImage(t *testing.T) {
	defer func(old time.Duration) { retryBaseWait = old }(retryBaseWait)
	retryBaseWait = time.Millisecond

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/pic":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("png-bytes"))
		case "/flaky":
			if requests == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("jpg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()

	// The extension follows the Content-Type, and no .part file is left.
	path, n, err := saveImage(srv.URL+"/pic", filepath.Join(dir, "a - 0001.jpg"))
	if err != nil || n != 9 || path != filepath.Join(dir, "a - 0001.png") {
		t.Fatalf("saveImage = %q, %d, %v", path, n, err)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part file left behind")
	}

	// A second call finds the existing .png and makes no request.
	before := requests
	if again, _, err := saveImage(srv.URL+"/pic", filepath.Join(dir, "a - 0001.jpg")); err != nil || again != path || requests != before {
		t.Errorf("re-save = %q, %v, %d new requests", again, err, requests-before)
	}

	// A 404 fails at once, without retrying.
	before = requests
	_, _, err = saveImage(srv.URL+"/missing", filepath.Join(dir, "b.jpg"))
	if err == nil || requests-before != 1 {
		t.Errorf("404: err=%v after %d requests, want an error after 1", err, requests-before)
	}
	// The URL is already shown next to the error, so it isn't repeated in it.
	if err != nil && (strings.Contains(err.Error(), srv.URL) || err.Error() != "server returned 404 Not Found") {
		t.Errorf("404 error = %q, want %q", err, "server returned 404 Not Found")
	}

	// A 502 is retried and then succeeds.
	requests = 0
	if _, _, err := saveImage(srv.URL+"/flaky", filepath.Join(dir, "c.jpg")); err != nil || requests != 2 {
		t.Errorf("502 then 200: err=%v after %d requests, want success after 2", err, requests)
	}
}
