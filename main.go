package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

// downloadAlbum dispatches a single /album/ URL.
// isCandid forces the post into /candids/ regardless of page title.
func downloadAlbum(albumURL string, downloadsDir string, finalizeWithZip bool, isCandid bool) {
	pageSource := getContents(albumURL)
	rawBytes, err := io.ReadAll(pageSource)
	if err != nil {
		panic(err)
	}

	info := parsePageInfo(getTitle(bytes.NewReader(rawBytes)))

	if isCandid || info.IsCandid {
		if isCandid && info.PostName == "" && info.AlbumName != "" {
			info.PostName = info.AlbumName
			info.AlbumName = ""
		}
		downloadCandidPost(albumURL, rawBytes, info, downloadsDir)
		return
	}

	downloadProperAlbum(albumURL, rawBytes, info, downloadsDir, finalizeWithZip)
}

// downloadProperAlbum saves a full photo set.
// Destination: /photos/<Model> - <Album>/
// File names:  <albumID> - <seq>.jpg
func downloadProperAlbum(albumURL string, rawBytes []byte, info PageInfo, downloadsDir string, finalizeWithZip bool) {
	// Extract album ID from URL (segment after "/album/")
	urlParts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
	albumID := ""
	for i, p := range urlParts {
		if p == "album" && i+1 < len(urlParts) {
			albumID = urlParts[i+1]
			break
		}
	}

	imagesFound := crawlAlbumImages(bytes.NewReader(rawBytes))
	albumDate, dateErr := getAlbumDate(bytes.NewReader(rawBytes))

	fmt.Printf("Found %q set from %s — %d image(s). Downloading...\n", info.AlbumName, info.ModelName, len(imagesFound))

	albumDir := downloadsDir + "/photos/" + info.ModelName + " - " + info.AlbumName
	checkAndCreateDir(albumDir)

	var wg sync.WaitGroup
	var mu sync.Mutex
	imagesDownloaded := make([]string, len(imagesFound))
	total := len(imagesFound)

	for i, imageURL := range imagesFound {
		wg.Add(1)
		go func(i int, imageURL string) {
			defer wg.Done()
			imageOutput := albumDir + "/" + albumID + " - " + fmt.Sprintf("%04d", i+1) + ".jpg"
			b, _ := saveImage(imageURL, imageOutput)
			imagesDownloaded[i] = imageOutput
			mu.Lock()
			fmt.Printf("[%04d/%04d] — %.2f MB\n", i+1, total, float64(b)/1024/1024)
			mu.Unlock()
		}(i, imageURL)
	}

	wg.Wait()

	if dateErr == nil {
		for _, imgPath := range imagesDownloaded {
			os.Chtimes(imgPath, albumDate, albumDate)
		}
	} else {
		fmt.Println("Warning: could not determine album date:", dateErr)
	}

	if finalizeWithZip {
		if err := ZipFiles(albumDir+"/"+info.AlbumName+".zip", imagesDownloaded); err != nil {
			panic(err)
		}
	}

	fmt.Println("Done!")
}

// downloadCandidPost saves images from a candid or blog post (one-image or multi-image).
//
// Structure:
//
//	One-image   -> candids/<Model>/<postID> - <postName> - 0001.jpg  (flat)
//	Multi-image -> candids/<Model>/<postID> - <postName>/0001.jpg    (folder)
func downloadCandidPost(albumURL string, rawBytes []byte, info PageInfo, downloadsDir string) {
	parts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
	postID := ""
	urlSlug := ""
	for i, p := range parts {
		if p == "album" && i+1 < len(parts) {
			postID = parts[i+1]
			if i+2 < len(parts) && parts[i+2] != "" {
				urlSlug = sanitizeName(parts[i+2])
			}
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
		postName = postID
	}

	// Scrape images: try full-album links first (multi-image inner pages),
	// fall back to data-picture-url (one-image pages).
	imagesFound := crawlAlbumImages(bytes.NewReader(rawBytes))
	if len(imagesFound) == 0 {
		imagesFound = crawlCandidImages(bytes.NewReader(rawBytes))
	}

	if len(imagesFound) == 0 {
		fmt.Printf("Candid post %s/%s — no images found, skipping\n", modelName, postID)
		return
	}

	modelDir := downloadsDir + "/candids/" + modelName
	checkAndCreateDir(modelDir)

	fmt.Printf("Candid post %s/%s (%s) — %d image(s)\n", modelName, postID, postName, len(imagesFound))

	if len(imagesFound) == 1 {
		imageOutput := fmt.Sprintf("%s/%s - %s - 0001.jpg", modelDir, postID, postName)
		b, _ := saveImage(imagesFound[0], imageOutput)
		fmt.Printf("[0001/0001] — %.2f MB\n", float64(b)/1024/1024)
		return
	}

	postDir := fmt.Sprintf("%s/%s - %s", modelDir, postID, postName)
	checkAndCreateDir(postDir)

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := len(imagesFound)

	for i, imageURL := range imagesFound {
		wg.Add(1)
		go func(i int, imageURL string) {
			defer wg.Done()
			imageOutput := fmt.Sprintf("%s/%s - %s - %04d.jpg", postDir, postID, postName, i+1)
			b, _ := saveImage(imageURL, imageOutput)
			mu.Lock()
			fmt.Printf("[%04d/%04d] — %.2f MB\n", i+1, total, float64(b)/1024/1024)
			mu.Unlock()
		}(i, imageURL)
	}

	wg.Wait()
}

// downloadBlogPost routes a blog post URL through the standard candid pipeline.
// Blog posts share the same /album/<id>/<slug>/ URL shape and image layout.
func downloadBlogPost(postURL string, downloadsDir string) {
	downloadAlbum(postURL, downloadsDir, false, true)
}

func main() {
	err := godotenv.Load()
	if err != nil {
		panic(err)
	}

	downloadsDir := os.Getenv("DOWNLOADSDIR")
	args := os.Args
	albumURL := args[1]
	finalizeWithZip := args[len(args)-1] == "-z"

	checkAndCreateDir(downloadsDir)
	checkAndCreateDir(downloadsDir + "/photos")
	checkAndCreateDir(downloadsDir + "/candids")

	switch {
	case strings.Contains(albumURL, "/album/"):
		// Single album/post page
		downloadAlbum(albumURL, downloadsDir, finalizeWithZip, false)

	case strings.Contains(albumURL, "/photos/"):
		// All albums from a model's dedicated listing page.
		// Extract modelName from URL path (e.g. /girls/lulumei/photos/...).
		photoParts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
		photoModel := ""
		for i, p := range photoParts {
			if (p == "girls" || p == "members") && i+1 < len(photoParts) {
				photoModel = photoParts[i+1]
				break
			}
		}
		albumLinks := getAllAlbumLinks(albumURL, photoModel)
		fmt.Println("Found", len(albumLinks), "albums")
		isCandid := strings.Contains(albumURL, "/candids/")
		for _, link := range albumLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, isCandid)
		}

	default:
		// Main model page — sweep dedicated listing pages, then paginated
		// feed for blog posts only (feed album posts are already covered by
		// the dedicated photosets page).
		parts := strings.Split(strings.TrimSuffix(albumURL, "/"), "/")
		modelName := parts[len(parts)-1]

		fmt.Printf("Content base: %s (model: %s)\n", albumURL, modelName)

		// 1. Dedicated candids page
		candidLinks := getAllAlbumLinks(strings.TrimSuffix(albumURL, "/")+"/photos/view/candids/", modelName)
		fmt.Println("Found", len(candidLinks), "candid posts")
		for _, link := range candidLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, true)
		}

		// 3. Paginated main feed — blog posts only.
		allBlogLinks := []string{}
		seenBlogs := map[string]bool{}

		offset := 0
		for {
			var pageURL string
			if offset == 0 {
				pageURL = albumURL
			} else {
				pageURL = fmt.Sprintf("%s?offset=%d", albumURL, offset)
			}

			pageSource := getContents(pageURL)
			rawBytes, err := io.ReadAll(pageSource)
			if err != nil {
				break
			}

			for _, link := range crawlBlogLinks(bytes.NewReader(rawBytes), modelName) {
				if !seenBlogs[link] {
					seenBlogs[link] = true
					allBlogLinks = append(allBlogLinks, link)
				}
			}

			nextOffset := getNextOffset(bytes.NewReader(rawBytes))
			if nextOffset < 0 || nextOffset == offset {
				break
			}
			offset = nextOffset
		}

		fmt.Println("Found", len(allBlogLinks), "blog posts")
		for _, link := range allBlogLinks {
			downloadBlogPost(link, downloadsDir)
		}
	}

	fmt.Println("Done!")
}
