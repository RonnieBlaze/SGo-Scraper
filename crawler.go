package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	gohtml "golang.org/x/net/html"
)

// crawlAlbumImages scrapes full-resolution .jpg links from <a href> tags on proper album pages.
func crawlAlbumImages(rawContents io.Reader) []string {
	z := gohtml.NewTokenizer(rawContents)
	var imagesFound []string

	for tt := z.Next(); ; tt = z.Next() {
		switch tt {
		case gohtml.ErrorToken:
			return imagesFound
		case gohtml.StartTagToken:
			t := z.Token()
			if t.Data != "a" {
				continue
			}
			link := getValueFromAttribute(t, "href")
			if strings.HasPrefix(link, "https") && strings.Contains(link, ".jpg") {
				imagesFound = append(imagesFound, link)
			}
		}
	}
}

// crawlCandidImages scrapes full-resolution images from a candid or blog post page
// via <a data-picture-url="...">. This is the only image source on one-image post
// pages; <img> tags only carry cache thumbnails there.
func crawlCandidImages(rawContents io.Reader) []string {
	z := gohtml.NewTokenizer(rawContents)
	var imagesFound []string
	seen := map[string]bool{}

	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return imagesFound
		}
		if tt != gohtml.StartTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "a" {
			continue
		}
		raw := getValueFromAttribute(t, "data-picture-url")
		if raw == "" {
			continue
		}
		u := html.UnescapeString(raw)
		if u == "" {
			continue
		}
		if !seen[u] {
			seen[u] = true
			imagesFound = append(imagesFound, u)
		}
	}
}

// dataOriginalRe matches the FULL data-original URL (including query string /
// signature) inside <script type="x-custom-image"> blocks. The tokenizer treats
// those blocks as opaque text so we scan raw bytes instead.
//
// KEY: use a plain " in the character class — not \" — because this is a raw
// backtick string where backslash has no special meaning outside of regexp syntax.
var dataOriginalRe = regexp.MustCompile(`data-original="(https?://[^"]+)"`)

// crawlBlogImagesRegex extracts full-resolution image URLs from raw page bytes.
// Used as a fallback when the page embeds images inside <script type="x-custom-image">.
func crawlBlogImagesRegex(rawBytes []byte) []string {
	seen := map[string]bool{}
	var result []string

	for _, m := range dataOriginalRe.FindAllSubmatch(rawBytes, -1) {
		u := html.UnescapeString(string(m[1]))
		if u == "" {
			continue
		}
		if !seen[u] {
			seen[u] = true
			result = append(result, u)
		}
	}
	return result
}

// albumLinkPattern matches /girls/<name>/album/<id>/<slug>/?.
var albumLinkPattern = regexp.MustCompile(`/(girls|members)/([^/]+)/album/`)

// crawlAlbums collects album links from a page, restricted to modelName.
// Passing an empty modelName disables the filter.
func crawlAlbums(rawContents io.Reader, modelName string) []string {
	z := gohtml.NewTokenizer(rawContents)
	var albumsFound []string
	seen := map[string]bool{}

	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return albumsFound
		}
		if tt != gohtml.StartTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "a" {
			continue
		}
		link := getValueFromAttribute(t, "href")
		if link == "" {
			continue
		}
		if !strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "https://www.suicidegirls.com") {
			continue
		}
		m := albumLinkPattern.FindStringSubmatch(link)
		if m == nil {
			continue
		}
		if modelName != "" && m[2] != modelName {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.suicidegirls.com" + link
		}
		if !seen[link] {
			seen[link] = true
			albumsFound = append(albumsFound, link)
		}
	}
}

// getAllAlbumLinks paginates through a listing URL and returns all album links for modelName.
func getAllAlbumLinks(modelURL string, modelName string) []string {
	var allAlbums []string
	seen := map[string]bool{}
	offset := 0

	for {
		var pageURL string
		if offset == 0 {
			pageURL = modelURL
		} else {
			pageURL = fmt.Sprintf("%s?offset=%d", modelURL, offset)
		}
		pageSource := getContents(pageURL)
		rawBytes, _ := io.ReadAll(pageSource)
		batch := crawlAlbums(bytes.NewReader(rawBytes), modelName)
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))

		for _, link := range batch {
			if !seen[link] {
				seen[link] = true
				allAlbums = append(allAlbums, link)
			}
		}
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	return allAlbums
}

// blogLinkPattern matches /members/<name>/blog/<id>/<slug>/ or /girls/<name>/blog/...
var blogLinkPattern = regexp.MustCompile(`/(members|girls)/([^/]+)/blog/(\d+)/`)

// crawlBlogLinks collects blog post links for modelName from a page.
func crawlBlogLinks(rawContents io.Reader, modelName string) []string {
	z := gohtml.NewTokenizer(rawContents)
	var linksFound []string
	seen := map[string]bool{}

	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return linksFound
		}
		if tt != gohtml.StartTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "a" {
			continue
		}
		link := getValueFromAttribute(t, "href")
		if !strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "https://www.suicidegirls.com") {
			continue
		}
		m := blogLinkPattern.FindStringSubmatch(link)
		if m == nil || m[2] != modelName {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.suicidegirls.com" + link
		}
		if !seen[link] {
			seen[link] = true
			linksFound = append(linksFound, link)
		}
	}
}

func getTitle(rawContents io.Reader) string {
	z := gohtml.NewTokenizer(rawContents)
	for tt := z.Next(); ; tt = z.Next() {
		switch tt {
		case gohtml.ErrorToken:
			return ""
		case gohtml.StartTagToken:
			t := z.Token()
			if t.Data != "title" {
				continue
			}
			z.Next()
			return z.Token().Data
		}
	}
}

// PageInfo holds parsed metadata from a page title.
type PageInfo struct {
	ModelName string // e.g. Psylunar
	PostName  string // e.g. Christmas Vibes (empty for proper albums)
	AlbumName string // e.g. Honey dump (empty for candid posts)
	IsCandid  bool
}

// parsePageInfo parses the raw page title into a PageInfo.
// Two known formats:
//   Proper album: "Model - Photo Album Name | SuicideGirls"
//   Candid post:  "PostName by Model | SuicideGirls"
func parsePageInfo(rawTitle string) PageInfo {
	if idx := strings.Index(rawTitle, " - Photo Album "); idx != -1 {
		model := strings.TrimSpace(rawTitle[:idx])
		rest := rawTitle[idx+len(" - Photo Album "):]
		name := strings.TrimSpace(strings.SplitN(rest, "|", 2)[0])
		return PageInfo{ModelName: sanitizeName(model), AlbumName: sanitizeName(name), IsCandid: false}
	}

	if idx := strings.LastIndex(rawTitle, " by "); idx != -1 {
		postName := strings.TrimSpace(rawTitle[:idx])
		rest := rawTitle[idx+len(" by "):]
		model := strings.TrimSpace(strings.SplitN(rest, "|", 2)[0])
		return PageInfo{ModelName: sanitizeName(model), PostName: sanitizeName(postName), IsCandid: true}
	}

	fmt.Println("Warning: unrecognised title format:", rawTitle)
	return PageInfo{ModelName: sanitizeName(rawTitle)}
}

// getAlbumInfo is kept for backward compatibility where only model/album names are needed.
func getAlbumInfo(rawContents io.Reader) (modelName string, albumName string) {
	info := parsePageInfo(getTitle(rawContents))
	if info.AlbumName != "" {
		return info.ModelName, info.AlbumName
	}
	return info.ModelName, "Unknown"
}

func sanitizeName(s string) string {
	replacer := strings.NewReplacer("/", "-", `\`, "-", ":", "-", "*", "-", "?", "", `"`, "", "<", "", ">", "", "|", "-", ".", "")
	return strings.TrimSpace(replacer.Replace(s))
}

func getAlbumDate(rawContents io.Reader) (time.Time, error) {
	z := gohtml.NewTokenizer(rawContents)
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			break
		}
		if tt == gohtml.StartTagToken {
			t := z.Token()
			if t.Data == "time" {
				z.Next()
				text := strings.TrimSpace(z.Token().Data)
				if text != "" {
					if parsed, err := time.Parse("Jan 2, 2006", text); err == nil {
						return parsed, nil
					}
					if parsed, err := time.Parse("Jan 2", text); err == nil {
						return parsed.AddDate(time.Now().Year(), 0, 0), nil
					}
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("date not found in page")
}

// newAuthedClient builds an http.Client with the SG session cookies attached.
// Used for both page fetches and image downloads so that paywalled (p=1) CDN
// URLs receive the session credentials they need.
func newAuthedClient(target string) *http.Client {
	sessionidCookie := os.Getenv("SESSIONIDTOKEN")
	sgcsrftoken := os.Getenv("SGCSRFTOKEN")
	rsciVid := os.Getenv("RSCIVID")

	jar, _ := cookiejar.New(nil)
	cookieData := []struct{ name, value string }{
		{"sessid", sessionidCookie},
		{"sgcsrftoken", sgcsrftoken},
		{"rscivid", rsciVid},
	}
	var cookies []*http.Cookie
	for _, c := range cookieData {
		if c.value == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:   c.name,
			Value:  c.value,
			Path:   "/",
			Domain: ".suicidegirls.com",
		})
	}

	for _, base := range []string{target, "https://www.suicidegirls.com/", "https://suicidegirls.com/"} {
		if u, err := url.Parse(base); err == nil {
			jar.SetCookies(u, cookies)
		}
	}
	return &http.Client{Jar: jar}
}

func getContents(link string) io.Reader {
	client := newAuthedClient(link)
	req, _ := http.NewRequest("GET", link, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://www.suicidegirls.com/")
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp.Body
}

func getValueFromAttribute(t gohtml.Token, attr string) string {
	for _, a := range t.Attr {
		if a.Key == attr {
			return a.Val
		}
	}
	return ""
}

// getNextOffset reads <link rel="next" href="..."> and returns the offset N,
// or -1 when no such tag exists (last page).
func getNextOffset(rawContents io.Reader) int {
	z := gohtml.NewTokenizer(rawContents)
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return -1
		}
		if tt != gohtml.StartTagToken && tt != gohtml.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "link" {
			continue
		}
		if getValueFromAttribute(t, "rel") == "next" {
			href := getValueFromAttribute(t, "href")
			if idx := strings.Index(href, "offset="); idx != -1 {
				raw := href[idx+len("offset="):]
				if amp := strings.IndexByte(raw, '&'); amp != -1 {
					raw = raw[:amp]
				}
				if n, err := strconv.Atoi(raw); err == nil {
					return n
				}
			}
		}
	}
}

// resolveContentBase looks for the Photos nav link to find the canonical content URL.
// Handles /members/ profiles whose photos live under /girls/ instead.
func resolveContentBase(rawContents io.Reader, inputURL string) string {
	z := gohtml.NewTokenizer(rawContents)
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			break
		}
		if tt != gohtml.StartTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "a" {
			continue
		}
		href := getValueFromAttribute(t, "href")
		if !strings.HasSuffix(href, "photos/") && !strings.HasSuffix(href, "photos") {
			continue
		}
		if !strings.Contains(href, "/girls/") && !strings.Contains(href, "/members/") {
			continue
		}
		base := strings.TrimSuffix(href, "photos/")
		base = strings.TrimSuffix(base, "photos")
		base = strings.TrimSuffix(base, "/")
		if !strings.HasPrefix(base, "http") {
			if strings.HasPrefix(base, "/") {
				base = "https://www.suicidegirls.com" + base
			} else {
				base = "https://www.suicidegirls.com/" + base
			}
		}
		return base
	}
	return strings.TrimSuffix(inputURL, "/")
}
