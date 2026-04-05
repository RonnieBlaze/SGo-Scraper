package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
	"regexp"

	"golang.org/x/net/html"
)

func crawlImages(rawContents io.Reader) []string {
	z := html.NewTokenizer(rawContents)
	imagesFound := []string{}

	for {
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			return imagesFound
		case tt == html.StartTagToken:
			t := z.Token()
			isAnchor := t.Data == "a"
			if !isAnchor {
				continue
			}
			link := getValueFromAttribute(t, "href")
			if link == "" {
				continue
			}
			hasProto := strings.Index(link, "https://") == 0 && strings.Index(link, ".jpg") > 0
			if hasProto {
				imagesFound = append(imagesFound, link)
			}
		}
	}
}

var albumLinkPattern = regexp.MustCompile(`/(girls|members)/[^/]+/album/\d+/[^/]+/$`)

func crawlAlbums(rawContents io.Reader) []string {
	z := html.NewTokenizer(rawContents)
	albumsFound := []string{}
	seen := map[string]bool{}

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return albumsFound
		}
		if tt == html.StartTagToken {
			t := z.Token()
			if t.Data != "a" {
				continue
			}
			link := getValueFromAttribute(t, "href")
			if link == "" {
				continue
			}
			// reject mailto, javascript, and any other non-HTTP links
			if !strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "https://www.suicidegirls.com") {
				continue
			}
			if !albumLinkPattern.MatchString(link) {
				continue
			}
			// normalize to absolute
			if !strings.HasPrefix(link, "http") {
				link = "https://www.suicidegirls.com" + link
			}
			if !seen[link] {
				seen[link] = true
				albumsFound = append(albumsFound, link)
			}
		}
	}
}

func getAllAlbumLinks(modelURL string) []string {
	allAlbums := []string{}
	seen := map[string]bool{}
	offset := 0

	for {
		pageURL := fmt.Sprintf("%s?partial=true&offset=%d", modelURL, offset)
		pageSource := getContents(pageURL)
		batch := crawlAlbums(pageSource)

		if len(batch) == 0 {
			break
		}

		for _, link := range batch {
			if !seen[link] {
				seen[link] = true
				allAlbums = append(allAlbums, link)
			}
		}

		offset += len(batch)
	}

	return allAlbums
}

func getTitle(rawContents io.Reader) string {
	z := html.NewTokenizer(rawContents)
	defaultTitle := ""
	for {
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			return defaultTitle
		case tt == html.StartTagToken:
			t := z.Token()
			isTitle := t.Data == "title"
			if !isTitle {
				continue
			}
			z.Next()
			title := z.Token()
			return title.Data
		}
	}
}

func getAlbumInfo(rawContents io.Reader) (modelName string, albumName string) {
	title := getTitle(rawContents)
	s := strings.Split(title, " Photo Album: ")
	ss := strings.Split(s[1], " | SuicideGirls")
	modelName = sanitizeName(s[0])
	albumName = sanitizeName(ss[0])
	return
}

func sanitizeName(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", "\"", "", "<", "", ">", "", "|", "-",
	)
	return strings.TrimSpace(replacer.Replace(s))
}

func getAlbumDate(rawContents io.Reader) (time.Time, error) {
	z := html.NewTokenizer(rawContents)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken {
			t := z.Token()
			if t.Data == "time" {
				z.Next()
				text := strings.TrimSpace(z.Token().Data)
				if text != "" {
					// try full date first
					if parsed, err := time.Parse("Jan 2, 2006", text); err == nil {
						return parsed, nil
					}
					// fall back to no-year format, assume current year
					if parsed, err := time.Parse("Jan 2", text); err == nil {
						return parsed.AddDate(time.Now().Year(), 0, 0), nil
					}
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("date not found in page")
}

func getContents(link string) io.Reader {
	sessionidCookie := os.Getenv("SESSIONIDTOKEN")
	sgcsrftoken := os.Getenv("SGCSRFTOKEN")
	rsciVid := os.Getenv("RSCI_VID")

	jar, _ := cookiejar.New(nil)
	cookieData := []struct{ name, value string }{
		{"sessid", sessionidCookie},
		{"sgcsrftoken", sgcsrftoken},
		{"rsci_vid", rsciVid},
	}

	var cookies []*http.Cookie
	for _, c := range cookieData {
		cookies = append(cookies, &http.Cookie{
			Name:   c.name,
			Value:  c.value,
			Path:   "/",
			Domain: "www.suicidegirls.com",
		})
	}
	u, _ := url.Parse(link)
	jar.SetCookies(u, cookies)
	fmt.Println(jar.Cookies(u))

	client := &http.Client{
		Jar: jar,
	}

	req, _ := http.NewRequest("GET", link, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://www.suicidegirls.com/")
	resp, err := client.Do(req)
	fmt.Println(resp)

	if err != nil {
		panic(err)
	}

	return resp.Body
}

func getValueFromAttribute(t html.Token, attr string) string {
	val := ""
	for _, a := range t.Attr {
		if a.Key == attr {
			val = a.Val
		}
	}

	return val
}
