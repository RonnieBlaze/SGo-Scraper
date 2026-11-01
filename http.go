package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ── shared client ─────────────────────────────────────────────────────────────

var (
	clientOnce   sync.Once
	sharedClient *http.Client
)

// authedClient returns the one HTTP client used for every request. It carries
// the session cookies from .env and reuses connections between requests.
func authedClient() *http.Client {
	clientOnce.Do(func() {
		rsciVid := os.Getenv("RSCI_VID")
		if rsciVid == "" {
			rsciVid = os.Getenv("RSCIVID") // older .env files
		}

		jar, _ := cookiejar.New(nil)
		cookieData := []struct{ name, value string }{
			{"sessid", os.Getenv("SESSIONIDTOKEN")},
			{"sgcsrftoken", os.Getenv("SGCSRFTOKEN")},
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
		for _, base := range []string{"https://www.suicidegirls.com", "https://suicidegirls.com"} {
			if u, err := url.Parse(base); err == nil {
				jar.SetCookies(u, cookies)
			}
		}
		sharedClient = &http.Client{Jar: jar, Timeout: 120 * time.Second}
	})
	return sharedClient
}

func newRequest(link, accept string) (*http.Request, error) {
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", getUserAgent())
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://www.suicidegirls.com/")
	return req, nil
}

// ── retries ───────────────────────────────────────────────────────────────────

const maxAttempts = 3

// retryBaseWait is the pause before the first retry; it grows with each
// attempt. Tests shorten it.
var retryBaseWait = 5 * time.Second

// permanentError marks a failure that retrying won't fix, such as a 404 or a
// local file error.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func permanent(err error) error { return permanentError{err} }

// withRetry runs fn up to maxAttempts times, pausing between attempts, until
// it succeeds or returns a permanent error. Network errors and 5xx responses
// are retried.
func withRetry(what string, fn func() error) error {
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = fn()
		if err == nil && attempt > 1 {
			fmt.Printf("\r%s\r%s\n", strings.Repeat(" ", 85), okText(fmt.Sprintf("[retry ok] %s — succeeded on attempt %d", what, attempt)))
		}
		var pe permanentError
		if err == nil || errors.As(err, &pe) {
			return err
		}
		if attempt < maxAttempts {
			wait := retryBaseWait * time.Duration(attempt)
			fmt.Printf("\r%s\r%s\n", strings.Repeat(" ", 85), warnText(fmt.Sprintf("[retry %d/%d in %s] %s — %v", attempt, maxAttempts-1, wait, what, err)))
			time.Sleep(wait)
		}
	}
	return err
}

// checkResponse exits on a 429 (the watcher pauses and retries) and turns any
// other non-2xx status into an error. Only 5xx errors are retried.
func checkResponse(resp *http.Response, link string) error {
	if resp.StatusCode == 429 {
		fmt.Printf("\n%s\n", paint(ansiBold+ansiRed, fmt.Sprintf("[429] Too Many Requests: %s. Exiting scraper...", link)))
		os.Exit(429)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// No URL here: the retry and failure messages already show it.
	err := fmt.Errorf("server returned %s", resp.Status)
	if resp.StatusCode >= 500 {
		return err
	}
	return permanent(err)
}

// ── pages ─────────────────────────────────────────────────────────────────────

// getContentsE fetches a page and returns its body, retrying brief network
// problems. It returns an error for a failed request or a non-2xx status.
func getContentsE(link string) ([]byte, error) {
	var body []byte
	err := withRetry(link, func() error {
		req, err := newRequest(link, "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		if err != nil {
			return permanent(err)
		}
		resp, err := authedClient().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkResponse(resp, link); err != nil {
			return err
		}
		body, err = io.ReadAll(resp.Body)
		return err
	})
	return body, err
}

var loggedInUserRe = regexp.MustCompile(`sg-user_name="([^"]+)"`)

// checkLogin makes sure the session cookies in .env still log in. A
// logged-out session still gets ordinary pages back, which would otherwise
// show up as a run of "no images found" failures or preview-size images.
// Setting LOGIN_CHECK=off in .env skips the check.
func checkLogin() error {
	if strings.TrimSpace(os.Getenv("SESSIONIDTOKEN")) == "" {
		return errors.New("SESSIONIDTOKEN is not set in .env")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LOGIN_CHECK")), "off") {
		return nil
	}
	body, err := getContentsE("https://www.suicidegirls.com/")
	if err != nil {
		return fmt.Errorf("could not load the home page to check the login: %v", err)
	}
	if m := loggedInUserRe.FindSubmatch(body); len(m) > 1 {
		fmt.Println(okText("Logged in as:"), itemText(string(m[1])))
		return nil
	}
	return errors.New("the site doesn't show you as logged in; your cookies have probably expired")
}

// ── images ────────────────────────────────────────────────────────────────────

var imageExts = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"image/avif": ".avif",
}

// imageExtFor returns the file extension for a Content-Type header, or "" if
// the type isn't a known image type.
func imageExtFor(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return imageExts[strings.ToLower(mt)]
}

// existingImage returns the path of an already-downloaded copy of output,
// under its own extension or any image extension, or "" if there is none.
func existingImage(output string) string {
	base := strings.TrimSuffix(output, filepath.Ext(output))
	for _, ext := range []string{filepath.Ext(output), ".jpg", ".png", ".webp", ".gif", ".avif"} {
		if fi, err := os.Stat(base + ext); err == nil && !fi.IsDir() && fi.Size() > 0 {
			return base + ext
		}
	}
	return ""
}

// saveImage downloads imageURL to output and returns the path it was saved
// to. The extension is corrected from the response's Content-Type, so a PNG
// requested as "x.jpg" is saved as "x.png". Files already on disk are left
// alone, and new files are written to a ".part" file first so an interrupted
// download never looks finished.
func saveImage(imageURL string, output string) (string, int64, error) {
	if existing := existingImage(output); existing != "" {
		return existing, 0, nil
	}
	var saved string
	var n int64
	err := withRetry(imageURL, func() error {
		var err error
		saved, n, err = saveImageOnce(imageURL, output)
		return err
	})
	return saved, n, err
}

func saveImageOnce(imageURL string, output string) (string, int64, error) {
	req, err := newRequest(imageURL, "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	if err != nil {
		return "", 0, permanent(err)
	}
	resp, err := authedClient().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp, imageURL); err != nil {
		return "", 0, err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(ct), "text/html") {
		return "", 0, permanent(fmt.Errorf("image URL returned HTML (likely ad redirect): %s", ct))
	}
	if ext := imageExtFor(ct); ext != "" {
		output = strings.TrimSuffix(output, filepath.Ext(output)) + ext
	}

	partial := output + ".part"
	img, err := os.Create(partial)
	if err != nil {
		return "", 0, permanent(err)
	}
	n, err := io.Copy(img, resp.Body)
	if closeErr := img.Close(); err == nil && closeErr != nil {
		err = permanent(closeErr)
	}
	if err == nil {
		if renameErr := os.Rename(partial, output); renameErr != nil {
			err = permanent(renameErr)
		}
	}
	if err != nil {
		os.Remove(partial)
		return "", n, err
	}

	if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
		if t, err := http.ParseTime(lastMod); err == nil {
			os.Chtimes(output, t, t)
		}
	}
	return output, n, nil
}
