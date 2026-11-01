package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	gohtml "golang.org/x/net/html"
)

var albumLinkPattern = regexp.MustCompile(`/((?:girls|members))/([^/]+)/album/(\d+)(?:/[^"'#?]+)?/?`)
var memberAlbumLinkPattern = regexp.MustCompile(`/members/([^/]+)/album/(\d+)(?:/[^"'#?]+)?/?`)
var blogLinkPattern = regexp.MustCompile(`/(?:members|girls)/([^/]+)/blog/(\d+)(?:/[^"'#?]+)?/?`)
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

func getGroupThreadTotalComments(threadURL string) int {
	rawBytes, err := getContentsE(strings.TrimSuffix(threadURL, "/") + "/comments/all?lazy=1")
	if err != nil {
		return -1
	}
	m := regexp.MustCompile(`(?i)(\d[\d,]*)\s+comment`).FindSubmatch(rawBytes)
	if len(m) < 2 {
		return -1
	}
	n := strings.ReplaceAll(string(m[1]), ",", "")
	v, err := strconv.Atoi(n)
	if err != nil {
		return -1
	}
	return v
}

func getAllGroupThreadImageBuckets(threadURL string) []GroupThreadImageBucket {
	seenImgs := map[string]map[string]bool{}
	orderedImgs := map[string][]string{} // keeps page order so numbering is stable
	bucketMeta := map[string]GroupThreadImageBucket{}
	var bucketOrder []string
	base := strings.TrimSuffix(threadURL, "/")
	baseURL := base + "/comments/all?lazy=1"
	totalComments := -1
	offset := 0
	useFallback := false
	for {
		var pageURL string
		if useFallback {
			pageURL = fmt.Sprintf("%s/comments/?offset=%d&count=600&lazy=1", base, offset)
		} else if offset == 0 {
			pageURL = baseURL
		} else {
			pageURL = fmt.Sprintf("%s&offset=%d", baseURL, offset)
		}
		fmt.Println(dimText(fmt.Sprintf("[group] fetching offset=%d fallback=%t", offset, useFallback)))
		pauseBeforeListingPage()
		rawBytes, err := getContentsE(pageURL)
		if err != nil {
			if useFallback {
				break
			}
			useFallback = true
			offset = 0
			if totalComments < 0 {
				totalComments = getGroupThreadTotalComments(threadURL)
				if totalComments > 0 {
					fmt.Println(dimText(fmt.Sprintf("[group] discovered total comments: %d", totalComments)))
				}
			}
			continue
		}
		pageBuckets := crawlGroupThreadImageBuckets(bytes.NewReader(rawBytes))
		pageNewImages := 0
		for _, bucket := range pageBuckets {
			key := bucket.CommentID
			if _, ok := seenImgs[key]; !ok {
				seenImgs[key] = map[string]bool{}
				bucketMeta[key] = GroupThreadImageBucket{
					CommentID:   bucket.CommentID,
					Username:    bucket.Username,
					CommentText: bucket.CommentText,
				}
				bucketOrder = append(bucketOrder, key)
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
				if !seenImgs[key][img] {
					seenImgs[key][img] = true
					orderedImgs[key] = append(orderedImgs[key], img)
					pageNewImages++
				}
			}
		}
		seenPosts := len(bucketMeta)
		totalImages := 0
		for key := range bucketMeta {
			totalImages += len(seenImgs[key])
		}
		if totalComments > 0 {
			fmt.Println(dimText(fmt.Sprintf("[group] progress: comments %d/%d, posts with images=%d, images=%d, last page posts=%d, last page new images=%d", offset, totalComments, seenPosts, totalImages, len(pageBuckets), pageNewImages)))
		} else {
			fmt.Println(dimText(fmt.Sprintf("[group] progress: comments offset=%d, posts with images=%d, images=%d, last page posts=%d, last page new images=%d", offset, seenPosts, totalImages, len(pageBuckets), pageNewImages)))
		}
		if useFallback {
			if len(pageBuckets) == 0 {
				break
			}
			offset += 600
			if totalComments > 0 && offset >= totalComments {
				break
			}
			continue
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 {
			if len(pageBuckets) == 0 {
				useFallback = true
				offset = 0
				if totalComments < 0 {
					totalComments = getGroupThreadTotalComments(threadURL)
					if totalComments > 0 {
						fmt.Println(dimText(fmt.Sprintf("[group] discovered total comments: %d", totalComments)))
					}
				}
				continue
			}
			break
		}
		if nextOffset == offset {
			break
		}
		offset = nextOffset
	}
	var result []GroupThreadImageBucket
	for _, key := range bucketOrder {
		meta := bucketMeta[key]
		meta.Images = orderedImgs[key]
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

// crawlCacheImages extracts permanent /cache/ image URLs from the page HTML.
// Each <li class="photo-container"> has two URLs: the <a href> points to an
// expiring /temp/ signed URL (403s after TTL), while the <img src> inside the
// <noscript> block points to a permanent /cache/ URL. This function collects
// the latter, which never require a fresh signed token.
func crawlCacheImages(rawContents io.Reader) []string {
	z := gohtml.NewTokenizer(rawContents)
	var imagesFound []string
	seen := map[string]bool{}
	inImageSection := false
	depth := 0 // article nesting depth
	inArticle := false
	for tt := z.Next(); ; tt = z.Next() {
		if tt == gohtml.ErrorToken {
			return imagesFound
		}
		switch tt {
		case gohtml.StartTagToken, gohtml.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "article":
				if !inArticle {
					inArticle = true
					depth = 1
				} else {
					depth++
				}
			case "section":
				if !inArticle {
					continue
				}
				for _, a := range t.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "image-section") {
						inImageSection = true
						break
					}
				}
			case "noscript":
				if !inImageSection {
					continue
				}
				raw := getValueFromAttribute(t, "data-retina")
				if raw == "" {
					raw = getValueFromAttribute(t, "data-src")
				}
				if raw == "" {
					continue
				}
				u := html.UnescapeString(raw)
				if u != "" && !seen[u] {
					seen[u] = true
					imagesFound = append(imagesFound, u)
				}
			}
		case gohtml.EndTagToken:
			t := z.Token()
			switch t.Data {
			case "section":
				inImageSection = false
			case "article":
				if inArticle {
					depth--
					if depth == 0 {
						// Past the post's own article — stop.
						return imagesFound
					}
				}
			}
		}
	}
}

// getAlbumInfoImages fetches full-resolution image URLs for a multi-image candid
// post via the get_album_info API. Returns nil for single-image posts (the HTML
// crawl chain handles those fine) and on any error, so callers fall back
// to the existing HTML-crawl chain unchanged.
func getAlbumInfoImages(postID string) []string {
	apiURL := fmt.Sprintf(
		"https://www.suicidegirls.com/api/get_album_info/%s/?geometries=2432,1216",
		postID,
	)
	req, err := newRequest(apiURL, "*/*")
	if err != nil {
		return nil
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := authedClient().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var result struct {
		Photos []struct {
			AlbumPhotoID int               `json:"album_photo_id"`
			URLs         map[string]string `json:"urls"`
		} `json:"photos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	// Single-image posts are handled by crawlCacheImages; skip the API for them.
	if len(result.Photos) <= 1 {
		return nil
	}
	// Photos come back in insertion order from the API; sort by album_photo_id
	// to guarantee correct sequence (the "number" field is always 0).
	photos := result.Photos
	for i := 1; i < len(photos); i++ {
		for j := i; j > 0 && photos[j].AlbumPhotoID < photos[j-1].AlbumPhotoID; j-- {
			photos[j], photos[j-1] = photos[j-1], photos[j]
		}
	}
	var images []string
	for _, p := range photos {
		if u := p.URLs["2432"]; u != "" {
			images = append(images, u)
		} else if u := p.URLs["1216"]; u != "" {
			images = append(images, u)
		}
	}
	return images
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
		if m == nil || (modelName != "" && !strings.EqualFold(m[2], modelName)) {
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
		if m == nil || (modelName != "" && !strings.EqualFold(m[1], modelName)) {
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
		if m == nil || (modelName != "" && !strings.EqualFold(m[1], modelName)) {
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

// pauseBeforeListingPage waits DOWNLOAD_DELAY_MS before loading a listing or
// comment page. Without it, listing pages went out back-to-back (especially
// now that finished items are skipped instantly), and the candids listing
// sometimes answered a quick burst with a 500 error.
func pauseBeforeListingPage() {
	if delay := getDownloadDelay(); delay > 0 {
		time.Sleep(delay)
	}
}

// getAllLinks follows a listing's ?offset= pages and collects the links that
// crawl finds on each. If a page fails to load, it's recorded as a failure and
// the links found so far are returned, so the rest of the run carries on.
func getAllLinks(listURL string, crawl func(io.Reader) []string) []string {
	var all []string
	seen := map[string]bool{}
	offset := 0
	for {
		pageURL := listURL
		if offset > 0 {
			pageURL = fmt.Sprintf("%s?offset=%d", listURL, offset)
		}
		pauseBeforeListingPage()
		rawBytes, err := getContentsE(pageURL)
		if err != nil {
			recordFailure(pageURL, fmt.Errorf("listing page failed, later pages skipped: %v", err))
			break
		}
		for _, link := range crawl(bytes.NewReader(rawBytes)) {
			if !seen[link] {
				seen[link] = true
				all = append(all, link)
			}
		}
		nextOffset := getNextOffset(bytes.NewReader(rawBytes))
		if nextOffset < 0 || nextOffset == offset {
			break
		}
		offset = nextOffset
	}
	return all
}

func getAllAlbumLinks(modelURL string, modelName string) []string {
	return getAllLinks(modelURL, func(r io.Reader) []string { return crawlAlbums(r, modelName) })
}

func getAllMemberAlbumLinks(modelURL string, modelName string) []string {
	return getAllLinks(modelURL, func(r io.Reader) []string { return crawlMemberAlbums(r, modelName) })
}

func getAllBlogLinks(modelURL string, modelName string) []string {
	return getAllLinks(modelURL, func(r io.Reader) []string { return crawlBlogLinks(r, modelName) })
}

func getAllVideoLinks(modelURL string) []string {
	return getAllLinks(modelURL, crawlVideoLinks)
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
	fmt.Println(warnText("Warning: unrecognised title format: " + rawTitle))
	return PageInfo{ModelName: sanitizeName(cleanTitle)}
}

func sanitizeName(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", `"`, "", "<", "", ">", "", "|", "-",
	)

	s = replacer.Replace(s)

	// Collapse all whitespaces (including newlines, tabs, carriage returns)
	// and trim leading/trailing space.
	s = strings.Join(strings.Fields(s), " ")

	// Windows does not allow filenames/folders ending in periods or spaces.
	s = strings.TrimRight(s, ". ")

	return s
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
							// The site leaves the year off recent dates. Use the
							// current year unless that puts the date in the future
							// (e.g. a December album seen in January).
							now := time.Now()
							d := parsed.AddDate(now.Year(), 0, 0)
							if d.After(now) {
								d = d.AddDate(-1, 0, 0)
							}
							return d, nil
						}
					}
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("date not found in page")
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
