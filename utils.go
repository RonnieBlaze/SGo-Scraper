package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func checkAndCreateDir(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(path, os.ModePerm)
	}
}

func digitsLen(n int) int {
	return len(strconv.Itoa(n))
}

func leftPad(s string, padStr string, pLen int) string {
	return strings.Repeat(padStr, pLen-len(s)) + s
}

// saveImage downloads url to output using an authenticated HTTP client so that
// paywalled CDN content (p=1 signed URLs) is served correctly rather than
// redirecting to the hotlink/ad image.
func saveImage(url string, output string) (int64, error) {
	client := newAuthedClient(url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://www.suicidegirls.com/")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("image request failed: %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(strings.ToLower(ct), "text/html") {
		return 0, fmt.Errorf("image URL returned HTML (likely ad redirect): %s", ct)
	}

	img, err := os.Create(output)
	if err != nil {
		return 0, err
	}
	defer img.Close()

	n, err := io.Copy(img, resp.Body)
	if err != nil {
		return n, err
	}
	if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
		if t, err := http.ParseTime(lastMod); err == nil {
			os.Chtimes(output, t, t)
		}
	}
	return n, nil
}

func ZipFiles(filename string, files []string) error {
	newfile, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer newfile.Close()

	zipWriter := zip.NewWriter(newfile)
	defer zipWriter.Close()

	for _, file := range files {
		zipfile, err := os.Open(file)
		if err != nil {
			return err
		}
		defer zipfile.Close()

		info, err := zipfile.Stat()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err = io.Copy(writer, zipfile); err != nil {
			return err
		}
	}
	return nil
}
