package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ogTitleRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']+)["']`)

func titleCaseModelName(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "/"))
	if s == "" {
		return "Unknown"
	}
	parts := strings.Fields(strings.ReplaceAll(s, "-", " "))
	for i, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(strings.ToLower(p))
		if len(r) == 0 {
			continue
		}
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}

func parseVideoInfo(videoURL string, rawBytes []byte, expectedModel string) (string, string, string) {
	parts := strings.Split(strings.TrimSuffix(videoURL, "/"), "/")
	videoID := ""
	videoSlug := ""
	for i, p := range parts {
		if p == "videos" && i+1 < len(parts) {
			videoID = parts[i+1]
			if i+2 < len(parts) && parts[i+2] != "" {
				videoSlug = sanitizeName(parts[i+2])
			}
			break
		}
	}
	if videoID == "" {
		videoID = sanitizeName(parts[len(parts)-1])
	}

	htmlStr := string(rawBytes)
	postTitle := ""
	modelName := titleCaseModelName(expectedModel)
	modelName = sanitizeName(modelName)

	if m := ogTitleRe.FindStringSubmatch(htmlStr); len(m) > 1 {
		og := strings.TrimSpace(m[1])
		if idx := strings.LastIndex(og, " by "); idx != -1 {
			postTitle = sanitizeName(strings.TrimSpace(og[:idx]))
			if modelName == "Unknown" || modelName == "" {
				rest := strings.TrimSpace(og[idx+len(" by "):])
				rest = strings.Split(rest, " | ")[0]
				rest = strings.Split(rest, " - ")[0]
				modelName = titleCaseModelName(rest)
			}
		} else {
			var ogParts []string
			if strings.Contains(og, " | ") {
				ogParts = strings.Split(og, " | ")
			} else if strings.Contains(og, " - ") {
				ogParts = strings.Split(og, " - ")
			}
			if len(ogParts) > 1 {
				postTitle = sanitizeName(ogParts[0])
				if modelName == "Unknown" || modelName == "" {
					candidate := strings.TrimSpace(ogParts[1])
					if !strings.EqualFold(candidate, "SuicideGirls") && !strings.EqualFold(candidate, "SGirls") {
						modelName = titleCaseModelName(candidate)
					} else if len(ogParts) > 2 {
						modelName = titleCaseModelName(ogParts[2])
					}
				}
			} else {
				postTitle = sanitizeName(og)
			}
		}
	}

	if postTitle == "" {
		title := strings.TrimSpace(getTitle(bytes.NewReader(rawBytes)))
		if idx := strings.LastIndex(title, " by "); idx != -1 {
			postTitle = sanitizeName(strings.TrimSpace(title[:idx]))
			if modelName == "Unknown" || modelName == "" {
				rest := strings.TrimSpace(title[idx+len(" by "):])
				rest = strings.Split(rest, " | ")[0]
				rest = strings.Split(rest, " - ")[0]
				modelName = titleCaseModelName(rest)
			}
		} else {
			var titleParts []string
			if strings.Contains(title, " | ") {
				titleParts = strings.Split(title, " | ")
			} else if strings.Contains(title, " - ") {
				titleParts = strings.Split(title, " - ")
			}
			if len(titleParts) > 1 {
				postTitle = sanitizeName(titleParts[0])
				if modelName == "Unknown" || modelName == "" {
					candidate := strings.TrimSpace(titleParts[1])
					if !strings.EqualFold(candidate, "SuicideGirls") && !strings.EqualFold(candidate, "SGirls") {
						modelName = titleCaseModelName(candidate)
					}
				}
			}
		}
	}

	if postTitle == "" {
		postTitle = videoSlug
	}
	if postTitle == "" {
		postTitle = videoID
	}

	// Fallback to scanning page HTML for model profile links if modelName is still Unknown/empty
	if modelName == "Unknown" || modelName == "" {
		loggedInUser := ""
		if um := loggedInUserRe.FindSubmatch(rawBytes); len(um) > 1 {
			loggedInUser = string(um[1])
		}

		allMatches := memberHrefRe.FindAllSubmatch(rawBytes, -1)
		for _, m := range allMatches {
			if len(m) > 1 {
				candidate := string(m[1])
				if loggedInUser != "" && strings.EqualFold(candidate, loggedInUser) {
					continue
				}
				modelName = titleCaseModelName(candidate)
				break
			}
		}
	}

	return videoID, truncateName(postTitle, 80), truncateName(modelName, 80)
}

// videoStallTimeout is how long a video download may go without the output
// file growing before it's treated as hung.
const videoStallTimeout = 2 * time.Minute

const defaultVideoTimeout = 180 * time.Minute

// getVideoTimeout returns the most time one video download may take, from
// VIDEO_TIMEOUT_MIN in .env.
func getVideoTimeout() time.Duration {
	if mins, err := strconv.Atoi(strings.TrimSpace(os.Getenv("VIDEO_TIMEOUT_MIN"))); err == nil && mins > 0 {
		return time.Duration(mins) * time.Minute
	}
	return defaultVideoTimeout
}

// watchForStall cancels the download if the file at path stops growing for
// videoStallTimeout, which catches a stream that hangs without erroring. The
// returned flag is set when that happens.
func watchForStall(ctx context.Context, path string, cancel context.CancelFunc) *atomic.Bool {
	var stalled atomic.Bool
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		lastSize := int64(-1)
		lastChange := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var size int64
				if fi, err := os.Stat(path); err == nil {
					size = fi.Size()
				}
				if size != lastSize {
					lastSize = size
					lastChange = time.Now()
				} else if time.Since(lastChange) >= videoStallTimeout {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	return &stalled
}

func downloadVideoPost(videoURL string, downloadsDir string, expectedModel string, currIndex, totalCount int) {
	// Skip a finished video without loading its page when the model is known
	// from the sweep or the URL. Otherwise the page-based check below runs.
	model := expectedModel
	if model == "" {
		model = extractModelFromURL(videoURL)
	}
	if model != "" {
		model = titleCaseModelName(model)
		if id := idAfter(videoURL, "videos"); knownDownloaded(model, id, "video") {
			printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Video %s/%s — already in database", model, id))
			return
		}
	}

	rawBytes, err := getContentsE(videoURL)
	if err != nil {
		recordFailure(videoURL, err)
		return
	}

	videoID, postTitle, modelName := parseVideoInfo(videoURL, rawBytes, expectedModel)

	if db, err := getModelDB(modelName); err == nil {
		done := isDownloaded(db, "video", videoID)
		db.Close()
		if done {
			printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Video %s/%s — already in database", modelName, videoID))
			return
		}
	}

	modelDir := filepath.Join(downloadsDir, modelName, "videos")

	// Skip if already downloaded.
	if findExistingEntry(modelDir, videoID+" - ", false) != "" {
		printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Video %s/%s — already on disk", modelName, videoID))
		if db, err := getModelDB(modelName); err == nil {
			markDownloaded(db, "video", videoID, postTitle)
			db.Close()
		}
		return
	}

	streamURL := crawlVideoStream(bytes.NewReader(rawBytes))
	if streamURL == "" {
		idxStr := formatIndex(currIndex, totalCount)
		fmt.Println(idxStr + errorText(fmt.Sprintf("Video %s/%s — could not find stream URL", modelName, videoID)))
		recordFailure(videoURL, fmt.Errorf("could not find stream URL"))
		return
	}

	checkAndCreateDir(modelDir)
	fmt.Println(dimText("VideoDir: " + modelDir))
	idxStr := formatIndex(currIndex, totalCount)
	output := filepath.Join(modelDir, fmt.Sprintf("%s - %s.mp4", videoID, postTitle))

	fmt.Println(idxStr + itemText(fmt.Sprintf("Video post %s/%s (%s) — downloading...", modelName, videoID, postTitle)))
	headers := fmt.Sprintf("Referer: https://www.suicidegirls.com/\r\nUser-Agent: %s\r\n", getUserAgent())
	// Write to a ".part" file and rename on success, so an interrupted run
	// never leaves a file the skip-on-rerun check treats as finished.
	partial := output + ".part"
	timeout := getVideoTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-headers", headers, "-y", "-i", streamURL, "-c", "copy", "-f", "mp4", partial)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stalled := watchForStall(ctx, partial, cancel)
	if err := cmd.Run(); err != nil {
		switch {
		case stalled.Load():
			err = fmt.Errorf("no data received for %s, gave up", videoStallTimeout)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			err = fmt.Errorf("took longer than %s, gave up (raise VIDEO_TIMEOUT_MIN in .env if needed)", timeout)
		default:
			err = fmt.Errorf("ffmpeg failed: %v", err)
		}
		fmt.Println(errorText(fmt.Sprintf("Video %s/%s — %v", modelName, videoID, err)))
		os.Remove(partial)
		recordFailure(videoURL, err)
		return
	}

	info, err := os.Stat(partial)
	if err != nil || info.Size() == 0 {
		fmt.Println(errorText(fmt.Sprintf("Video %s/%s — output file is missing or empty", modelName, videoID)))
		os.Remove(partial)
		recordFailure(videoURL, fmt.Errorf("output file is missing or empty"))
		return
	}

	if err := os.Rename(partial, output); err != nil {
		os.Remove(partial)
		recordFailure(videoURL, err)
		return
	}

	if db, err := getModelDB(modelName); err == nil {
		markDownloaded(db, "video", videoID, postTitle)
		db.Close()
	}
	runStats.downloaded++
	runStats.bytes += info.Size()
	// Videos have no progress bar, so confirm the finished file.
	fmt.Println(okText(fmt.Sprintf("Saved %s (%.2f MB)", filepath.Base(output), float64(info.Size())/1024/1024)))

	delay := getDownloadDelay()
	if delay > 0 {
		time.Sleep(delay)
	}

	//fmt.Println("Done!")
	fmt.Println()
}
