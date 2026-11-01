package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Exit codes other than 0 and 429. The watcher uses these to decide whether
// to retry a URL or park it in Incoming\Failed.
const (
	exitSomeFailed  = 3 // the run finished, but some items failed
	exitLoginFailed = 4 // the cookies in .env don't log in
	exitBadURL      = 5 // the URL isn't one the scraper handles
)

// downloadImages fetches urls into the files named by pathFor, up to 5 at a
// time with the configured delay between starts. It returns the path each
// image was saved to ("" where it failed) and how many failed.
func downloadImages(label string, urls []string, pathFor func(i int) string) ([]string, int) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	saved := make([]string, len(urls))
	total := len(urls)
	sem := make(chan struct{}, 5) // limit to 5 simultaneous downloads

	completed := 0
	failed := 0
	var totalBytes int64

	printProgress(label, 0, total, 0)
	delay := getDownloadDelay()
	for i, imageURL := range urls {
		sem <- struct{}{} // acquire slot before spawning
		if i > 0 && delay > 0 {
			time.Sleep(delay) // delay between download starts
		}
		wg.Add(1)
		go func(i int, imageURL string) {
			defer wg.Done()
			defer func() { <-sem }() // release slot when finished

			path, b, err := saveImage(imageURL, pathFor(i))

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Printf("\r%s\r%s\n", strings.Repeat(" ", 85), errorText(fmt.Sprintf("Error: %s [%04d/%04d] — %v", label, i+1, total, err)))
				failed++
			} else {
				saved[i] = path
				totalBytes += b
			}
			completed++
			printProgress(label, completed, total, totalBytes)
		}(i, imageURL)
	}

	wg.Wait()
	runStats.bytes += totalBytes
	return saved, failed
}

func downloadAlbum(albumURL string, downloadsDir string, finalizeWithZip bool, isCandid bool, currIndex, totalCount int) {
	// The ID and model are in the URL, so a finished item can be skipped
	// without loading its page. If the URL's model doesn't match the history
	// name, this finds nothing and the page-based check below still runs.
	if model, id := extractModelFromURL(albumURL), idAfter(albumURL, "album", "blog"); knownDownloaded(model, id, "album", "candid") {
		kind := "Album"
		if isCandid || strings.Contains(albumURL, "/blog/") {
			kind = "Candid post"
		}
		printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] %s %s/%s — already in database", kind, model, id))
		return
	}

	rawBytes, err := getContentsE(albumURL)
	if err != nil {
		recordFailure(albumURL, err)
		return
	}

	info := parsePageInfo(getTitle(bytes.NewReader(rawBytes)))

	if totalCount <= 1 && extractModelFromURL(albumURL) == "" && info.ModelName != "" {
		fmt.Println("Model:", headingText(info.ModelName))
		fmt.Println(dimText("ModelDir: " + filepath.Join(downloadsDir, info.ModelName)))
		fmt.Println()
	}

	if isCandid || info.IsCandid {
		if isCandid && info.PostName == "" && info.AlbumName != "" {
			info.PostName = info.AlbumName
			info.AlbumName = ""
		}
		downloadCandidPost(albumURL, rawBytes, info, downloadsDir, currIndex, totalCount)
		return
	}

	downloadProperAlbum(albumURL, rawBytes, info, downloadsDir, finalizeWithZip, currIndex, totalCount)
}

func downloadProperAlbum(albumURL string, rawBytes []byte, info PageInfo, downloadsDir string, finalizeWithZip bool, currIndex, totalCount int) {
	urlParts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
	albumID := ""
	for i, p := range urlParts {
		if p == "album" && i+1 < len(urlParts) {
			albumID = urlParts[i+1]
			break
		}
	}

	if db, err := getModelDB(info.ModelName); err == nil {
		done := isDownloaded(db, "album", albumID)
		db.Close()
		if done {
			printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Album %s/%s — already in database", info.ModelName, albumID))
			return
		}
	}

	imagesFound := crawlAlbumImages(bytes.NewReader(rawBytes))
	albumDate, dateErr := getAlbumDate(bytes.NewReader(rawBytes))

	idxStr := formatIndex(currIndex, totalCount)
	fmt.Println(idxStr + itemText(fmt.Sprintf("Found %q set from %s — %d image(s). Downloading...", info.AlbumName, info.ModelName, len(imagesFound))))

	albumDir := filepath.Join(downloadsDir, info.ModelName, "photos", info.ModelName+" - "+info.AlbumName)
	fmt.Println(dimText("AlbumDir: " + albumDir)) //debug info for looking directory name.
	checkAndCreateDir(albumDir)

	imagesDownloaded, failed := downloadImages("Downloading", imagesFound, func(i int) string {
		return filepath.Join(albumDir, fmt.Sprintf("%s - %04d.jpg", albumID, i+1))
	})
	total := len(imagesFound)

	switch {
	case total == 0:
		recordFailure(albumURL, fmt.Errorf("no images found"))
	case failed > 0:
		recordFailure(albumURL, fmt.Errorf("%d of %d images failed", failed, total))
	default:
		runStats.downloaded++
		if db, err := getModelDB(info.ModelName); err == nil {
			markDownloaded(db, "album", albumID, info.AlbumName)
			db.Close()
		}
	}

	if dateErr == nil {
		for _, imgPath := range imagesDownloaded {
			if imgPath != "" {
				os.Chtimes(imgPath, albumDate, albumDate)
			}
		}
	} else {
		fmt.Println(warnText(fmt.Sprint("Warning: could not determine album date: ", dateErr)))
	}

	if finalizeWithZip {
		var filtered []string
		for _, f := range imagesDownloaded {
			if f != "" {
				filtered = append(filtered, f)
			}
		}
		if err := ZipFiles(filepath.Join(albumDir, info.AlbumName+".zip"), filtered); err != nil {
			recordFailure(albumURL, fmt.Errorf("zipping failed: %v", err))
		}
	}

	if delay := getDownloadDelay(); failed < total && delay > 0 {
		time.Sleep(delay)
	}

	//fmt.Println("Albums Done.")
	fmt.Println()
}

func downloadCandidPost(albumURL string, rawBytes []byte, info PageInfo, downloadsDir string, currIndex, totalCount int) {
	parts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")

	postID := ""
	urlSlug := ""
	for i, p := range parts {
		switch p {
		case "album", "blog":
			if i+1 < len(parts) {
				postID = parts[i+1]
				if i+2 < len(parts) && parts[i+2] != "" {
					urlSlug = sanitizeName(parts[i+2])
				}
				if postID != "" {
					break
				}
			}
		}
		if postID != "" {
			break
		}
	}
	if postID == "" {
		postID = sanitizeName(parts[len(parts)-1])
	}

	modelName := info.ModelName
	if modelName == "" {
		for i, p := range parts {
			if (p == "girls" || p == "members") && i+1 < len(parts) {
				rawModel := sanitizeName(parts[i+1])
				if rawModel != "" {
					modelName = strings.ToUpper(rawModel[:1]) + rawModel[1:]
				}
				break
			}
		}
	}

	postName := info.PostName
	if postName == "" {
		postName = urlSlug
	}
	if postName == "" {
		postName = info.AlbumName
	}
	if postName == "" {
		postName = postID
	}
	postName = truncateName(postName, 80)

	if db, err := getModelDB(modelName); err == nil {
		done := isDownloaded(db, "candid", postID)
		db.Close()
		if done {
			printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Candid post %s/%s — already in database", modelName, postID))
			return
		}
	}

	// Use the API first; it returns permanent /cache/ URLs, not expiring /temp/ ones.
	imagesFound := getAlbumInfoImages(postID)
	if len(imagesFound) == 0 {
		imagesFound = crawlCacheImages(bytes.NewReader(rawBytes))
	}
	if len(imagesFound) == 0 {
		imagesFound = crawlAlbumImages(bytes.NewReader(rawBytes))
	}
	if len(imagesFound) == 0 {
		imagesFound = crawlCandidImages(bytes.NewReader(rawBytes))
	}
	if len(imagesFound) == 0 {
		imagesFound = crawlBlogImagesRegex(rawBytes)
	}
	if len(imagesFound) == 0 {
		printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("Candid post %s/%s — no images found, skipping", modelName, postID))
		return
	}

	modelDir := filepath.Join(downloadsDir, modelName, "candids")
	entryPrefix := postID + " - "

	// Single-image posts already on disk (e.g. from before the database
	// existed) are recorded and skipped. Multi-image posts reuse their existing
	// folder below and only fetch the images that are missing.
	if len(imagesFound) == 1 && findExistingEntry(modelDir, entryPrefix, false) != "" {
		printSkip(formatIndex(currIndex, totalCount), fmt.Sprintf("[skip] Candid post %s/%s — already on disk", modelName, postID))
		if db, err := getModelDB(modelName); err == nil {
			markDownloaded(db, "candid", postID, postName)
			db.Close()
		}
		return
	}
	checkAndCreateDir(modelDir)

	idxStr := formatIndex(currIndex, totalCount)
	fmt.Println(idxStr + itemText(fmt.Sprintf("Candid post %s/%s (%s) — %d image(s)", modelName, postID, postName, len(imagesFound))))

	var pathFor func(i int) string
	if len(imagesFound) == 1 {
		fmt.Println(dimText("ModelDir: " + modelDir))
		pathFor = func(int) string {
			return filepath.Join(modelDir, fmt.Sprintf("%s - %s - 0001.jpg", postID, postName))
		}
	} else {
		postDir := filepath.Join(modelDir, fmt.Sprintf("%s - %s", postID, postName))
		if existing := findExistingEntry(modelDir, entryPrefix, true); existing != "" {
			postDir = filepath.Join(modelDir, existing)
		}
		fmt.Println(dimText("PostDir: " + postDir)) //debug info for looking directory name.
		checkAndCreateDir(postDir)
		pathFor = func(i int) string {
			return filepath.Join(postDir, fmt.Sprintf("%s - %s - %04d.jpg", postID, postName, i+1))
		}
	}

	_, failed := downloadImages("Downloading", imagesFound, pathFor)
	if failed > 0 {
		recordFailure(albumURL, fmt.Errorf("%d of %d images failed", failed, len(imagesFound)))
	} else {
		runStats.downloaded++
		if db, err := getModelDB(modelName); err == nil {
			markDownloaded(db, "candid", postID, postName)
			db.Close()
		}
	}
	if delay := getDownloadDelay(); delay > 0 {
		time.Sleep(delay)
	}
	//fmt.Println("Done!")
	fmt.Println()
}

func downloadBlogPost(postURL string, downloadsDir string, currIndex, totalCount int) {
	downloadAlbum(postURL, downloadsDir, false, true, currIndex, totalCount)
}

func downloadGroupThread(threadURL string, downloadsDir string) {
	rawBytes, err := getContentsE(threadURL)
	if err != nil {
		recordFailure(threadURL, err)
		return
	}

	parts := strings.Split(strings.TrimSuffix(threadURL, "/"), "/")
	groupName := "group"
	threadID := "thread"
	for i, p := range parts {
		if p == "groups" && i+1 < len(parts) {
			groupName = sanitizeName(parts[i+1])
		}
		if p == "thread" && i+1 < len(parts) {
			threadID = sanitizeName(parts[i+1])
		}
	}

	rawTitle := strings.TrimSpace(strings.Split(getTitle(bytes.NewReader(rawBytes)), " by ")[0])
	threadTitle := sanitizeName(rawTitle)
	if threadTitle == "" {
		threadTitle = threadID
	}
	threadTitle = truncateName(threadTitle, 60)

	threadDir := filepath.Join(downloadsDir, "groups", groupName, threadID+" - "+threadTitle)

	buckets := getAllGroupThreadImageBuckets(threadURL)
	if len(buckets) == 0 {
		fmt.Println(dimText(fmt.Sprintf("Group thread %s/%s — no images found, skipping", groupName, threadID)))
		return
	}

	fmt.Println(itemText(fmt.Sprintf("Group thread %s/%s (%s) — %d post(s) with images", groupName, threadID, threadTitle, len(buckets))))
	fmt.Println(dimText("ThreadDir: " + threadDir)) //debug info for looking directory name.
	checkAndCreateDir(threadDir)

	existingEntries, _ := os.ReadDir(threadDir)

	for _, bucket := range buckets {
		if len(bucket.Images) == 0 {
			continue
		}

		commentSnippet := truncateName(sanitizeName(bucket.CommentText), 60)

		if bucket.CommentID != "" {
			if db, err := getGroupDB(); err == nil {
				done := isDownloaded(db, "group_comment", bucket.CommentID)
				db.Close()
				if done {
					printSkip("", fmt.Sprintf("[skip] Group thread %s/%s — comment %s already in database", groupName, threadID, bucket.CommentID))
					continue
				}
			}

			// Only treat the comment as done if every image is already on
			// disk; a partially downloaded comment fetches the rest.
			prefix := bucket.CommentID + " - "
			onDisk := 0
			for _, e := range existingEntries {
				if strings.HasPrefix(e.Name(), prefix) && !strings.HasSuffix(e.Name(), ".part") {
					onDisk++
				}
			}
			if onDisk >= len(bucket.Images) {
				printSkip("", fmt.Sprintf("[skip] Group thread %s/%s — comment %s already on disk", groupName, threadID, bucket.CommentID))
				if db, err := getGroupDB(); err == nil {
					markDownloaded(db, "group_comment", bucket.CommentID, commentSnippet)
					db.Close()
				}
				continue
			}
		}

		var baseName string
		if commentSnippet != "" {
			baseName = fmt.Sprintf("%s - %s - %s", bucket.CommentID, bucket.Username, commentSnippet)
		} else {
			baseName = fmt.Sprintf("%s - %s", bucket.CommentID, bucket.Username)
		}

		_, failed := downloadImages(baseName, bucket.Images, func(i int) string {
			return filepath.Join(threadDir, fmt.Sprintf("%s - %04d.jpg", baseName, i+1))
		})
		if failed > 0 {
			recordFailure(threadURL+" comment "+bucket.CommentID, fmt.Errorf("%d of %d images failed", failed, len(bucket.Images)))
		} else {
			runStats.downloaded++
			if bucket.CommentID != "" {
				if db, err := getGroupDB(); err == nil {
					markDownloaded(db, "group_comment", bucket.CommentID, commentSnippet)
					db.Close()
				}
			}
		}
		if delay := getDownloadDelay(); delay > 0 {
			time.Sleep(delay)
		}
	}
}

func extractModelFromURL(urlStr string) string {
	parts := strings.Split(strings.TrimSuffix(urlStr, "/"), "/")
	for i, p := range parts {
		if (p == "girls" || p == "members") && i+1 < len(parts) {
			return titleCaseModelName(parts[i+1])
		}
	}
	return ""
}

// The kinds of page the scraper handles; see routeFor.
const (
	routeGroupThread = "group thread"
	routeVideo       = "video"
	routeModelVideos = "model videos"
	routeAlbum       = "album"
	routePhotos      = "photos listing"
	routeModelBlog   = "model blog"
	routeBlogPost    = "blog post"
	routeProfile     = "model profile"
)

// routeFor works out which kind of page a URL is, or returns "" when it isn't
// a suicidegirls.com page the scraper handles. Unrecognised URLs used to be
// swept as a model profile, so a typo produced a confusing run.
func routeFor(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	if host := strings.ToLower(u.Hostname()); host != "www.suicidegirls.com" && host != "suicidegirls.com" {
		return ""
	}

	p := u.Path
	trimmed := strings.TrimSuffix(p, "/")
	isModel := strings.Contains(p, "/girls/") || strings.Contains(p, "/members/")
	switch {
	case strings.Contains(p, "/groups/") && strings.Contains(p, "/thread/"):
		return routeGroupThread
	case strings.Contains(p, "/videos/") && !isModel:
		return routeVideo
	case isModel && strings.HasSuffix(trimmed, "/videos"):
		return routeModelVideos
	case strings.Contains(p, "/album/"):
		return routeAlbum
	case strings.Contains(p, "/photos"):
		return routePhotos
	case isModel && strings.HasSuffix(trimmed, "/blog"):
		return routeModelBlog
	case strings.Contains(p, "/blog/") && !strings.HasSuffix(trimmed, "/blog"):
		return routeBlogPost
	}
	if _, _, ok := profileBase(rawURL); ok {
		return routeProfile
	}
	return ""
}

// profileBase returns the clean profile URL (no query or trailing slash) and
// model name for a "/girls/<name>/" or "/members/<name>/" URL.
func profileBase(rawURL string) (string, string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || (parts[0] != "girls" && parts[0] != "members") || parts[1] == "" {
		return "", "", false
	}
	return fmt.Sprintf("%s://%s/%s/%s", u.Scheme, u.Host, parts[0], parts[1]), parts[1], true
}

func main() {
	err := godotenv.Load()
	if err != nil {
		panic(err)
	}
	initColor()
	showSkips = !strings.EqualFold(strings.TrimSpace(os.Getenv("SHOW_SKIPS")), "off")
	start := time.Now()

	downloadsDir := os.Getenv("DOWNLOADSDIR")
	args := os.Args
	if len(args) < 2 {
		panic("usage: SGo-Scraper <url> [-z]")
	}

	albumURL := strings.TrimSpace(args[1])
	finalizeWithZip := args[len(args)-1] == "-z"

	fmt.Println(dimText("[" + time.Now().Format("2006-01-02 15:04:05") + "]"))
	fmt.Println("URL:", itemText(albumURL))

	route := routeFor(albumURL)
	if route == "" {
		fmt.Println(errorText("Error: this isn't a SuicideGirls URL the scraper handles."))
		fmt.Println("Use a model profile, photos, album, videos, blog or group thread URL on www.suicidegirls.com.")
		os.Exit(exitBadURL)
	}

	modelName := extractModelFromURL(albumURL)
	if modelName != "" {
		fmt.Println("MODEL:", headingText(modelName))
		fmt.Println(dimText("ModelDir: " + filepath.Join(downloadsDir, modelName)))
	}
	fmt.Println()

	if err := checkLogin(); err != nil {
		fmt.Println(errorText(fmt.Sprint("Error: ", err)))
		fmt.Println("Update the cookies in .env (see README), or set LOGIN_CHECK=off in .env to skip this check.")
		os.Exit(exitLoginFailed)
	}

	checkAndCreateDir(downloadsDir)

	switch route {
	case routeGroupThread:
		downloadGroupThread(albumURL, downloadsDir)

	case routeVideo:
		downloadVideoPost(albumURL, downloadsDir, "", 1, 1)

	case routeModelVideos:
		videoLinks := getAllVideoLinks(strings.TrimSuffix(albumURL, "/") + "/")
		printHeading(fmt.Sprintf("Found %d Videos", len(videoLinks)))
		for idx, link := range videoLinks {
			downloadVideoPost(link, downloadsDir, modelName, idx+1, len(videoLinks))
		}
		if modelName != "" {
			printFinished(modelName)
		}

	case routeAlbum:
		downloadAlbum(albumURL, downloadsDir, finalizeWithZip, false, 1, 1)

	case routePhotos:
		photoParts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
		photoModel := ""
		for i, p := range photoParts {
			if (p == "girls" || p == "members") && i+1 < len(photoParts) {
				photoModel = photoParts[i+1]
				break
			}
		}
		albumLinks := getAllAlbumLinks(albumURL, photoModel)
		isCandid := strings.Contains(albumURL, "/candids/")
		heading := "Photosets"
		if isCandid {
			heading = "Candid Posts"
		}
		printHeading(fmt.Sprintf("Found %d %s", len(albumLinks), heading))
		for idx, link := range albumLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, isCandid, idx+1, len(albumLinks))
		}
		if photoModel != "" {
			printFinished(titleCaseModelName(photoModel))
		}

	case routeModelBlog:
		blogLinks := getAllBlogLinks(strings.TrimSuffix(albumURL, "/"), modelName)
		printHeading(fmt.Sprintf("Found %d Blog Posts", len(blogLinks)))
		for idx, link := range blogLinks {
			downloadBlogPost(link, downloadsDir, idx+1, len(blogLinks))
		}
		if modelName != "" {
			printFinished(modelName)
		}

	case routeBlogPost:
		downloadBlogPost(albumURL, downloadsDir, 1, 1)

	case routeProfile:
		base, profileModel, _ := profileBase(albumURL)
		seen := map[string]bool{}

		photosetLinks := getAllAlbumLinks(base+"/photos/view/photosets/", profileModel)
		printHeading(fmt.Sprintf("Found %d Photosets", len(photosetLinks)))
		for idx, link := range photosetLinks {
			seen[link] = true
			downloadAlbum(link, downloadsDir, finalizeWithZip, false, idx+1, len(photosetLinks))
		}

		fmt.Println()
		candidLinks := getAllAlbumLinks(base+"/photos/view/candids/", profileModel)
		printHeading(fmt.Sprintf("Found %d Candid Posts", len(candidLinks)))
		for idx, link := range candidLinks {
			if seen[link] {
				continue
			}
			seen[link] = true
			downloadAlbum(link, downloadsDir, finalizeWithZip, true, idx+1, len(candidLinks))
		}

		fmt.Println()
		videoLinks := getAllVideoLinks(base + "/videos/")
		printHeading(fmt.Sprintf("Found %d Videos", len(videoLinks)))
		for idx, link := range videoLinks {
			if seen[link] {
				continue
			}
			seen[link] = true
			downloadVideoPost(link, downloadsDir, profileModel, idx+1, len(videoLinks))
		}

		fmt.Println()
		blogLinks := getAllBlogLinks(base+"/blog/", profileModel)
		printHeading(fmt.Sprintf("Found %d Blog Posts", len(blogLinks)))
		for idx, link := range blogLinks {
			if seen[link] {
				continue
			}
			seen[link] = true
			downloadBlogPost(link, downloadsDir, idx+1, len(blogLinks))
		}

		fmt.Println()
		printFinished(titleCaseModelName(profileModel))
	}

	fmt.Println()
	printSummary(time.Since(start))

	if len(failedItems) > 0 {
		fmt.Println()
		fmt.Println(errorText(fmt.Sprintf("%d item(s) failed and were not marked as downloaded; re-run to retry them:", len(failedItems))))
		for _, f := range failedItems {
			fmt.Println(errorText("  - " + f))
		}
		os.Exit(exitSomeFailed)
	}
}

func printProgress(prefix string, completed, total int, totalBytes int64) {
	if total <= 0 {
		return
	}
	width := 30
	percent := float64(completed) / float64(total)
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}

	var bar string
	if filled == width {
		bar = strings.Repeat("=", width)
	} else if filled > 0 {
		bar = strings.Repeat("=", filled-1) + ">" + strings.Repeat(" ", width-filled)
	} else {
		bar = strings.Repeat(" ", width)
	}

	// The bar is cyan while downloading and turns green when it's full.
	if completed == total {
		bar = okText(bar)
	} else {
		bar = paint(ansiCyan, bar)
	}

	sizeMB := float64(totalBytes) / 1024 / 1024
	fmt.Printf("\r%s: [%s] %d/%d (%d%%) | %s", prefix, bar, completed, total, int(percent*100), dimText(fmt.Sprintf("%.2f MB", sizeMB)))
	if completed == total {
		fmt.Println()
	}
}
