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
// Destination: /photos/<ModelName> - <AlbumName>/
func downloadProperAlbum(albumURL string, rawBytes []byte, info PageInfo, downloadsDir string, finalizeWithZip bool) {
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
			imageOutput := albumDir + "/" + fmt.Sprintf("%04d", i+1) + ".jpg"
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

// downloadCandidPost saves images from a candid post (one-image or multi-image).
//
// Structure:
//
//	One-image   → candids/<ModelName>/<postID> - <postName> - 0001.jpg  (flat)
//	Multi-image → candids/<ModelName>/<postID> - <postName>/0001.jpg   (folder)
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

// downloadBlogPost fetches a blog post page and saves all images it contains.
//
// Structure:
//
//	One-image   → candids/<ModelName>/<postID> - <postName> - 0001.jpg  (flat)
//	Multi-image → candids/<ModelName>/<postID> - <postName>/0001.jpg   (folder)
//
// Two image formats are handled:
//   - <a data-picture-url="...">  (older posts, parsed by crawlCandidImages)
//   - <script type="x-custom-image"> with data-original="..." inside
//     (newer posts; the tokenizer treats the script body as opaque text, so
//     crawlBlogImagesRegex scans the raw bytes with a regexp instead)
func downloadBlogPost(postURL string, downloadsDir string) {
	m := blogLinkPattern.FindStringSubmatch(postURL)
	if m == nil {
		fmt.Println("Warning: could not parse blog post URL:", postURL)
		return
	}

	// Capitalize model name for directory use.
	rawModel := sanitizeName(m[2])
	modelName := rawModel
	if modelName != "" {
		modelName = strings.ToUpper(modelName[:1]) + modelName[1:]
	}
	postID := m[3]
	parts := strings.Split(strings.TrimSuffix(postURL, "/"), "/")
	urlSlug := sanitizeName(parts[len(parts)-1])

	pageSource := getContents(postURL)
	rawBytes, err := io.ReadAll(pageSource)
	if err != nil {
		fmt.Println("Warning: could not fetch blog post:", postURL, err)
		return
	}

	// Use the actual page title as the post name; fall back to URL slug.
	info := parsePageInfo(getTitle(bytes.NewReader(rawBytes)))
	postName := info.PostName
	if postName == "" {
		postName = urlSlug
	}

	images := crawlCandidImages(bytes.NewReader(rawBytes))
	if len(images) == 0 {
		images = crawlBlogImagesRegex(rawBytes)
	}
	if len(images) == 0 {
		fmt.Println("No images found in:", postURL)
		return
	}

	modelDir := downloadsDir + "/candids/" + modelName
	checkAndCreateDir(modelDir)

	fmt.Printf("Blog post %s — %d image(s)\n", postName, len(images))

	// One image → flat file, same convention as candid posts.
	if len(images) == 1 {
		imageOutput := fmt.Sprintf("%s/%s - %s - 0001.jpg", modelDir, postID, postName)
		b, _ := saveImage(images[0], imageOutput)
		fmt.Printf("[0001/0001] — %.2f MB\n", float64(b)/1024/1024)
		return
	}

	// Multiple images → folder.
	postDir := fmt.Sprintf("%s/%s - %s", modelDir, postID, postName)
	checkAndCreateDir(postDir)

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := len(images)

	for i, imageURL := range images {
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

func main() {
	if err := godotenv.Load(); err != nil {
		panic(err)
	}

	downloadsDir := os.Getenv("DOWNLOADSDIR")
	args := os.Args
	albumURL := args[1]
	finalizeWithZip := args[len(args)-1] == "-z"

	checkAndCreateDir(downloadsDir)
	checkAndCreateDir(downloadsDir + "/photos")
	checkAndCreateDir(downloadsDir + "/candids")

	// modelNameFromURL returns the lowercase slug for URL-filter use only.
	modelNameFromURL := func(u string) string {
		parts := strings.Split(strings.TrimSuffix(u, "/"), "/")
		for i, p := range parts {
			if (p == "girls" || p == "members") && i+1 < len(parts) {
				return parts[i+1]
			}
		}
		return ""
	}

	switch {
	case strings.Contains(albumURL, "/album/"):
		downloadAlbum(albumURL, downloadsDir, finalizeWithZip, false)

	case strings.Contains(albumURL, "/photos/"):
		isCandid := strings.Contains(albumURL, "/photos/view/candids")
		modelName := modelNameFromURL(albumURL)
		albumLinks := getAllAlbumLinks(albumURL, modelName)
		fmt.Println("Found", len(albumLinks), "albums")
		for _, link := range albumLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, isCandid)
		}

	default:
		// Fetch the profile page once. For /members/ profiles that are SuicideGirls,
		// the nav exposes a "girls/<name>/photos" link — resolveContentBase extracts
		// that so photosets and candids use the correct /girls/ URL instead of 404ing
		// on the /members/ equivalent.
		profileSource := getContents(albumURL)
		profileBytes, err := io.ReadAll(profileSource)
		if err != nil {
			panic(err)
		}
		contentBase := resolveContentBase(bytes.NewReader(profileBytes), albumURL)
		modelName := modelNameFromURL(contentBase)

		fmt.Printf("Content base: %s (model: %s)\n", contentBase, modelName)

		// Proper photosets
		albumLinks := getAllAlbumLinks(contentBase+"/photos/view=photosets/", modelName)
		fmt.Println("Found", len(albumLinks), "albums")
		for _, link := range albumLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, false)
		}

		// Candids
		candidLinks := getAllAlbumLinks(contentBase+"/photos/view=candids/", modelName)
		fmt.Println("Found", len(candidLinks), "candid posts")
		for _, link := range candidLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip, true)
		}

		// Blog posts — sweep main feed using the original input URL (blog pagination
		// works regardless of members/ vs girls/ path) until rel="next" disappears.
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
			if nextOffset < 0 {
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
