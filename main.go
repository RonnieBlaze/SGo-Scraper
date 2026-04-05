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

func downloadAlbum(albumURL string, downloadsDir string, finalizeWithZip bool) {
	pageSource := getContents(albumURL)
	rawBytes, err := io.ReadAll(pageSource)
	if err != nil {
		panic(err)
	}

	modelName, albumName := getAlbumInfo(bytes.NewReader(rawBytes))
	imagesFound := crawlImages(bytes.NewReader(rawBytes))
	albumDate, dateErr := getAlbumDate(bytes.NewReader(rawBytes))

	fmt.Println("Found", albumName, "set from", modelName, "!")
	fmt.Println("Found", len(imagesFound), "images in set. Downloading...")

	albumDir := downloadsDir + "/" + modelName + " - " + albumName
	checkAndCreateDir(albumDir)

	var wg sync.WaitGroup
	var mu sync.Mutex
	imagesDownloaded := make([]string, len(imagesFound))
	total := len(imagesFound)

	for i, imageURL := range imagesFound {
		wg.Add(1)
		go func(i int, imageURL string) {
			defer wg.Done()
			imageOutput := albumDir + "/" + fmt.Sprintf("%03d", i) + ".jpg"
			b, _ := saveImage(imageURL, imageOutput)
			imagesDownloaded[i] = imageOutput

			mu.Lock()
			fmt.Printf("[%03d/%03d] %03d.jpg — %.2f MB\n", i+1, total, i, float64(b)/1024/1024)
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
		err := ZipFiles(albumDir+"/"+albumName+".zip", imagesDownloaded)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Done... Enjoy!")
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

	if strings.Contains(albumURL, "/photos/") {
		albumLinks := getAllAlbumLinks(albumURL)
		fmt.Println("Found", len(albumLinks), "albums")
		for _, link := range albumLinks {
			downloadAlbum(link, downloadsDir, finalizeWithZip)
		}
	} else {
		downloadAlbum(albumURL, downloadsDir, finalizeWithZip)
	}
}