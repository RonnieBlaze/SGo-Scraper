package main

import (
    "bytes"
    "fmt"
    "io"
    "os"
	"sync"
    "github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		panic(err)
	}

	downloadsDir := os.Getenv("DOWNLOADSDIR")
	args := os.Args
	albumURL := args[1]
	finalizeWithZip := args[len(args)-1] == "-z"

	pageSource := getContents(albumURL)
	// Read the body once into memory
	rawBytes, err := io.ReadAll(pageSource)
	if err != nil {
		panic(err)
	}

	// Each function gets its own fresh reader from the same bytes
	modelName, albumName := getAlbumInfo(bytes.NewReader(rawBytes))
	imagesFound := crawlImages(bytes.NewReader(rawBytes))

	fmt.Println("Found", albumName, "set from", modelName, "!")
	fmt.Println("Found", len(imagesFound), "images in set. Downloading...")

	albumDir := downloadsDir + "/" + modelName + " - " + albumName

	checkAndCreateDir(downloadsDir)
	checkAndCreateDir(albumDir)
	// imagesDownloaded := []string{}

	// for i, imageURL := range imagesFound {
		// imageOutput := albumDir + "/" + leftPad(strconv.Itoa(i), "0", digitsLen(len(imagesFound))-1) + ".jpg"
		// imageOutput := albumDir + "/" + fmt.Sprintf("%03d", i) + ".jpg"
		// fmt.Println(imageURL + " -> " + imageOutput)
		// imagesDownloaded = append(imagesDownloaded, imageOutput)

		// b, _ := saveImage(imageURL, imageOutput)
		// fmt.Println("File size:", b)
	// }
	
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

	if finalizeWithZip {
		err := ZipFiles(albumDir+"/"+albumName+".zip", imagesDownloaded)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Done... Enjoy!")
}
