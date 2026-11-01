package main

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var defaultDownloadDelay = 2000 * time.Millisecond
var defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

func getDownloadDelay() time.Duration {
	val := os.Getenv("DOWNLOAD_DELAY_MS")
	if val == "" {
		return defaultDownloadDelay
	}
	ms, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || ms < 0 {
		return defaultDownloadDelay
	}
	return time.Duration(ms) * time.Millisecond
}

func getUserAgent() string {
	ua := strings.TrimSpace(os.Getenv("USER_AGENT"))
	if ua != "" {
		return ua
	}
	return defaultUserAgent
}

func checkAndCreateDir(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(path, os.ModePerm)
	}
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
		if file == "" {
			continue
		}

		zipfile, err := os.Open(file)
		if err != nil {
			return err
		}

		info, err := zipfile.Stat()
		if err != nil {
			zipfile.Close()
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			zipfile.Close()
			return err
		}

		header.Name = filepath.Base(file)
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			zipfile.Close()
			return err
		}

		if _, err = io.Copy(writer, zipfile); err != nil {
			zipfile.Close()
			return err
		}

		zipfile.Close()
	}

	return nil
}

func getDBDir() string {
	exePath, err := os.Executable()
	var baseDir string
	if err == nil {
		baseDir = filepath.Dir(exePath)
	} else {
		baseDir = "." // Fallback to current working directory
	}
	dbDir := filepath.Join(baseDir, "modelsdb")
	checkAndCreateDir(dbDir)
	return dbDir
}

func modelDBPath(modelName string) string {
	return filepath.Join(getDBDir(), sanitizeName(modelName)+".db")
}

func getModelDB(modelName string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", modelDBPath(modelName))
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS downloads (
		type TEXT,
		item_id TEXT,
		title TEXT,
		downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (type, item_id)
	);`
	_, err = db.Exec(schema)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func getGroupDB() (*sql.DB, error) {
	dbDir := getDBDir()
	dbPath := filepath.Join(dbDir, "groups.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS downloads (
		type TEXT,
		item_id TEXT,
		title TEXT,
		downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (type, item_id)
	);`
	_, err = db.Exec(schema)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func isDownloaded(db *sql.DB, itemType string, itemID string) bool {
	if db == nil {
		return false
	}
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM downloads WHERE type = ? AND item_id = ?)", itemType, itemID).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// knownDownloaded reports whether itemID is recorded under any of types in
// modelName's history. Unlike getModelDB it never creates a database, so a
// guessed model name that doesn't match an existing history is harmless. It
// lets the scraper skip finished items without loading their pages.
func knownDownloaded(modelName, itemID string, types ...string) bool {
	if modelName == "" || itemID == "" {
		return false
	}
	if _, err := os.Stat(modelDBPath(modelName)); err != nil {
		return false
	}
	db, err := getModelDB(modelName)
	if err != nil {
		return false
	}
	defer db.Close()
	for _, t := range types {
		if isDownloaded(db, t, itemID) {
			return true
		}
	}
	return false
}

// idAfter returns the URL path segment that follows the first of segments
// found, e.g. "123" for ".../album/123/name/" with segment "album".
func idAfter(rawURL string, segments ...string) string {
	parts := strings.Split(strings.TrimSuffix(rawURL, "/"), "/")
	for i, p := range parts {
		for _, s := range segments {
			if p == s && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return ""
}

func markDownloaded(db *sql.DB, itemType string, itemID string, title string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec("INSERT OR REPLACE INTO downloads (type, item_id, title) VALUES (?, ?, ?)", itemType, itemID, title)
	return err
}

// findExistingEntry returns the name of the first entry in dir whose name
// starts with prefix (and is a directory when wantDir is set), ignoring
// unfinished ".part" downloads. It returns "" when there is none.
func findExistingEntry(dir, prefix string, wantDir bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && !strings.HasSuffix(e.Name(), ".part") && e.IsDir() == wantDir {
			return e.Name()
		}
	}
	return ""
}

// failedItems collects items that could not be fully downloaded. They are
// left out of the database so the next run retries them.
var failedItems []string

func recordFailure(item string, err error) {
	fmt.Println(errorText(fmt.Sprintf("Failed: %s — %v", item, err)))
	failedItems = append(failedItems, fmt.Sprintf("%s (%v)", item, err))
}

// runStats counts what happened during the run, for the summary at the end.
// Items are processed one at a time, so no locking is needed.
var runStats struct {
	downloaded int
	skipped    int
	bytes      int64
}

// showSkips controls whether "[skip]" lines are printed; SHOW_SKIPS=off in
// .env hides them. Skips are counted for the summary either way.
var showSkips = true

// printSkip reports an item that needed no download.
func printSkip(idxStr, msg string) {
	runStats.skipped++
	if showSkips {
		fmt.Println(idxStr + dimText(msg))
	}
}

// printSummary prints one line totalling the run.
func printSummary(elapsed time.Duration) {
	failed := fmt.Sprintf("Failed %d", len(failedItems))
	if len(failedItems) > 0 {
		failed = errorText(failed)
	}
	fmt.Printf("%s %s · %s · %s · %.2f MB\n",
		headingText(fmt.Sprintf("Done in %s —", elapsed.Round(time.Second))),
		okText(fmt.Sprintf("Downloaded %d", runStats.downloaded)),
		dimText(fmt.Sprintf("Skipped %d", runStats.skipped)),
		failed,
		float64(runStats.bytes)/1024/1024)
}

func formatIndex(curr, total int) string {
	if total <= 0 {
		return ""
	}
	width := len(strconv.Itoa(total))
	if width < 2 {
		width = 2
	}
	return indexText(fmt.Sprintf("[%0*d/%0*d]", width, curr, width, total)) + " "
}
