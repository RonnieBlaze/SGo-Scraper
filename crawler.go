package main

import (
	"bytes"
	"encoding/json"
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

var albumLinkPattern = regexp.MustCompile(`/((?:girls|members))/([^/]+)/album/(\d+)(?:/[^"'#?]+)?/?`)
var memberAlbumLinkPattern = regexp.MustCompile(`/members/([^/]+)/album/(\d+)(?:/[^"'#?]+)?/?`)
var blogLinkPattern = regexp.MustCompile(`/members/([^/]+)/blog/(\d+)(?:/[^"'#?]+)?/?`)
var videoLinkPattern = regexp.MustCompile(`/videos/(\d+)(?:/[^"'#?]+)?/?`)

// ── group thread ────────────────────────────────────────────────────────────
var memberHrefRe = regexp.MustCompile(`href="/(?:members|girls)/([^/"]+)/"`)
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

type GroupThreadImageBucket struct {
	CommentID   string
	Username    string
	CommentText string
	Images      []string
}

func stripHTML(b []byte) string {
	plain := htmlTagRe.ReplaceAll(b, []byte(" "))
	decoded := html.UnescapeString(string(plain))
	return strings.Join(strings.Fields(decoded), " ")
}

var commentTextMarker = []byte(`<div class="comment-text" data-comment-id="`)

func crawlGroupThreadImageBuckets(rawContents io.Reader) []GroupThreadImageBucket {
	rawBytes, err := io.ReadAll(rawContents)
	if err != nil {
		return nil
	}
	const marker = `<div class="flex-wrapper" data-comment-id="`
	parts := bytes.Split(rawBytes, []byte(marker))

	var buckets []GroupThreadImageBucket
	for _, part := range parts[1:] {
		idEnd := bytes.IndexByte(part, '"')
		if idEnd < 0 {
			continue
		}
		commentID := sanitizeName(string(part[:idEnd]))
		if commentID == "" {
			continue
		}
		ctIdx := bytes.Index(part, commentTextMarker)
		headerBlock := part
		if ctIdx > 0 {
			headerBlock = part[:ctIdx]
		}
		username := ""
		if um := memberHrefRe.FindSubmatch(headerBlock); len(um) > 1 {
			username = sanitizeName(string(um[1]))
		}
		if ctIdx < 0 {
			continue
		}
		openEnd := bytes.IndexByte(part[ctIdx:], '>')
		if openEnd < 0 {
			continue
		}
		body := part[ctIdx+openEnd+1:]
		if timeIdx := bytes.Index(body, []byte("<time>")); timeIdx > 0 {
			body = body[:timeIdx]
		}
		seen := map[string]bool{}
		var imgs []string
		for _, im := range dataOriginalRe.FindAllSubmatch(body, -1) {
			if len(im) < 2 {
				continue
			}
			u := html.UnescapeString(string(im[1]))
			if u != "" && !seen[u] {
				seen[u] = true
				imgs = append(imgs, u)
			}
		}
		if len(imgs) == 0 {
			continue
		}
		commentText := stripHTML(body)
		buckets = append(buckets, GroupThreadImageBucket{
			CommentID:   commentID,
			Username:    username,
			CommentText: commentText,
			Images:      imgs,
		})
	}
	return buckets
}

func getAllGroupThreadImageBuckets(threadURL string) []GroupThreadImageBucket {
	seenImgs := map[string]map[string]bool{}
	bucketMeta := map[string]GroupThreadImageBucket{}
	baseURL := strings.TrimSuffix(threadURL, "/") + "/comments/all?lazy=1"
	offset := 0
	for {
		var pageURL string
		if offset == 0 {
			pageURL = baseURL
		} else {
			pageURL = fmt.Sprintf("%s&offset=%d", baseURL, offset)
		}
		pageSource := getContents(pageURL)
		rawBytes, _ := io.ReadAll(pageSource)
		for _, bucket := range crawlGroupThreadImageBuckets(bytes.NewReader(rawBytes)) {
			key := bucket.CommentID
			if _, ok := seenImgs[key]; !ok {
				seenImgs[key] = map[string]bool{}
				bucketMeta[key] = GroupThreadImageBucket{
					CommentID:   bucket.CommentID,
					Username:    bucket.Username,
					CommentText: bucket.CommentText,
				}
			}
			meta := bucketMeta[key]
			if meta.Username == "" && bucket.Username != "" {
				meta.Username = bucket.Username
			}
			if meta.CommentText == "" && bucket.CommentText != "" {
				meta.CommentText = bucket.CommentText
			}
			bucketMeta[key] = meta
			for _, img := range bucket.Images {
				seenImgs[key][img] = true
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	var result []GroupThreadImageBucket
	for key, meta := range bucketMeta {
		for img := range seenImgs[key] {
			meta.Images = append(meta.Images, img)
		}
		result = append(result, meta)
	}
	return result
}

// ── image scrapers ───────────────────────────────────────────────────────────

func crawlAlbumImages(rawContents io.Reader) []string {
	z := gohtml.NewTokenizer(rawContents)
	var imagesFound []string
	seen := map[string]bool{}
	for tt := z.Next(); ; tt = z.Next() {
		switch tt {
		case gohtml.ErrorToken:
			return imagesFound
		case gohtml.StartTagToken:
			t := z.Token()
			if t.Data != "a" {
				continue
			}
			link := html.UnescapeString(getValueFromAttribute(t, "href"))
			if strings.HasPrefix(link, "https") && strings.Contains(strings.ToLower(link), ".jpg") {
				if !seen[link] {
					seen[link] = true
					imagesFound = append(imagesFound, link)
				}
			}
		}
	}
}

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
		if u != "" && !seen[u] {
			seen[u] = true
			imagesFound = append(imagesFound, u)
		}
	}
}

var dataOriginalRe = regexp.MustCompile(`data-original="(https?://[^"]+)"`)
var imageURLRe = regexp.MustCompile(`https?://[^"'<>\s]+\.(?:jpg|jpeg|png|webp)(?:\?[^"'<>\s]*)?`)

func crawlBlogImagesRegex(rawBytes []byte) []string {
	seen := map[string]bool{}
	var result []string
	for _, m := range dataOriginalRe.FindAllSubmatch(rawBytes, -1) {
		if len(m) < 2 {
			continue
		}
		u := html.UnescapeString(string(m[1]))
		if u != "" && !seen[u] {
			seen[u] = true
			result = append(result, u)
		}
	}
	if len(result) > 0 {
		return result
	}
	for _, m := range imageURLRe.FindAll(rawBytes, -1) {
		u := html.UnescapeString(string(m))
		if u != "" && !seen[u] {
			seen[u] = true
			result = append(result, u)
		}
	}
	return result
}

// ── video stream ─────────────────────────────────────────────────────────────

var videoSourcesRe = regexp.MustCompile(`"sources"\s*:\s*(\[[^\]]+\])`)
var videoFileRe = regexp.MustCompile(`"file"\s*:\s*"([^"]+)"`)
var hlsURLRe = regexp.MustCompile(`https?://[^"'<>\s]+\.m3u8(?:\?[^"'<>\s]*)?`)
var mp4URLRe = regexp.MustCompile(`https?://[^"'<>\s]+\.mp4(?:\?[^"'<>\s]*)?`)

func crawlVideoStream(rawContents io.Reader) string {
	rawBytes, err := io.ReadAll(rawContents)
	if err != nil {
		return ""
	}
	if m := videoSourcesRe.FindSubmatch(rawBytes); len(m) > 1 {
		var sources []map[string]any
		if err := json.Unmarshal(m[1], &sources); err == nil {
			for _, src := range sources {
				if file, ok := src["file"].(string); ok && strings.Contains(file, ".m3u8") {
					return html.UnescapeString(file)
				}
			}
			for _, src := range sources {
				if file, ok := src["file"].(string); ok && file != "" {
					return html.UnescapeString(file)
				}
			}
		}
	}
	if m := videoFileRe.FindSubmatch(rawBytes); len(m) > 1 {
		return html.UnescapeString(string(m[1]))
	}
	if m := hlsURLRe.Find(rawBytes); len(m) > 0 {
		return html.UnescapeString(string(m))
	}
	if m := mp4URLRe.Find(rawBytes); len(m) > 0 {
		return html.UnescapeString(string(m))
	}
	return ""
}

// ── link crawlers ─────────────────────────────────────────────────────────────

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
		if m == nil || (modelName != "" && m[2] != modelName) {
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

func crawlMemberAlbums(rawContents io.Reader, modelName string) []string {
	z := gohtml.NewTokenizer(rawContents)
	var found []string
	seen := map[string]bool{}
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return found
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
		m := memberAlbumLinkPattern.FindStringSubmatch(link)
		if m == nil || (modelName != "" && m[1] != modelName) {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.suicidegirls.com" + link
		}
		if !seen[link] {
			seen[link] = true
			found = append(found, link)
		}
	}
}

func crawlBlogLinks(rawContents io.Reader, modelName string) []string {
	z := gohtml.NewTokenizer(rawContents)
	var found []string
	seen := map[string]bool{}
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return found
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
		m := blogLinkPattern.FindStringSubmatch(link)
		if m == nil || (modelName != "" && m[1] != modelName) {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.suicidegirls.com" + link
		}
		if !seen[link] {
			seen[link] = true
			found = append(found, link)
		}
	}
}

func crawlVideoLinks(rawContents io.Reader) []string {
	z := gohtml.NewTokenizer(rawContents)
	var found []string
	seen := map[string]bool{}
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return found
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
		if videoLinkPattern.FindStringSubmatch(link) == nil {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.suicidegirls.com" + link
		}
		if !seen[link] {
			seen[link] = true
			found = append(found, link)
		}
	}
}

// ── paginated sweeps ──────────────────────────────────────────────────────────

func getAllAlbumLinks(modelURL string, modelName string) []string {
	var all []string
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
		for _, link := range crawlAlbums(bytes.NewReader(rawBytes), modelName) {
			if !seen[link] {
				seen[link] = true
				all = append(all, link)
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	return all
}

func getAllMemberAlbumLinks(modelURL string, modelName string) []string {
	var all []string
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
		for _, link := range crawlMemberAlbums(bytes.NewReader(rawBytes), modelName) {
			if !seen[link] {
				seen[link] = true
				all = append(all, link)
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	return all
}

func getAllBlogLinks(modelURL string, modelName string) []string {
	var all []string
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
		for _, link := range crawlBlogLinks(bytes.NewReader(rawBytes), modelName) {
			if !seen[link] {
				seen[link] = true
				all = append(all, link)
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	return all
}

func getAllVideoLinks(modelURL string) []string {
	var all []string
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
		for _, link := range crawlVideoLinks(bytes.NewReader(rawBytes)) {
			if !seen[link] {
				seen[link] = true
				all = append(all, link)
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			break
		}
		offset = nextOffset
	}
	return all
}

// ── page helpers ──────────────────────────────────────────────────────────────

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
			if z.Next() == gohtml.TextToken {
				return z.Token().Data
			}
		}
	}
}

type PageInfo struct {
	ModelName string
	PostName  string
	AlbumName string
	IsCandid  bool
}

func parsePageInfo(rawTitle string) PageInfo {
	cleanTitle := strings.TrimSpace(rawTitle)
	if idx := strings.Index(cleanTitle, "Photo Album"); idx != -1 {
		model := strings.TrimSpace(strings.TrimRight(cleanTitle[:idx], " -:|"))
		after := strings.TrimSpace(cleanTitle[idx+len("Photo Album"):])
		name := strings.TrimSpace(strings.TrimLeft(after, " -:|"))
		name = strings.TrimSpace(strings.SplitN(name, " | ", 2)[0])
		if byIdx := strings.LastIndex(name, " by "); byIdx != -1 {
			name = strings.TrimSpace(name[:byIdx])
		}
		return PageInfo{ModelName: sanitizeName(model), AlbumName: sanitizeName(name)}
	}
	if idx := strings.LastIndex(cleanTitle, " by "); idx != -1 {
		postName := strings.TrimSpace(cleanTitle[:idx])
		rest := cleanTitle[idx+len(" by "):]
		model := strings.TrimSpace(strings.SplitN(rest, " | ", 2)[0])
		return PageInfo{ModelName: sanitizeName(model), PostName: sanitizeName(postName), IsCandid: true}
	}
	fmt.Println("Warning: unrecognised title format:", rawTitle)
	return PageInfo{ModelName: sanitizeName(cleanTitle)}
}

func sanitizeName(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", `"`, "", "<", "", ">", "", "|", "-",
	)
	return strings.TrimSpace(replacer.Replace(s))
}

func truncateName(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return strings.TrimSpace(string(runes[:maxLen]))
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
				if z.Next() == gohtml.TextToken {
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
	}
	return time.Time{}, fmt.Errorf("date not found in page")
}

func newAuthedClient(target string) http.Client {
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
			Name: c.name, Value: c.value,
			Path: "/", Domain: ".suicidegirls.com",
		})
	}
	for _, base := range []string{target, "https://www.suicidegirls.com", "https://suicidegirls.com"} {
		if u, err := url.Parse(base); err == nil {
			jar.SetCookies(u, cookies)
		}
	}
	return http.Client{Jar: jar}
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
