package main

import (
	"fmt"
	"os"
	"strconv"
	"log"
	"regexp"
	
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
	modelName, albumName := getAlbumInfo(pageSource)
	imagesFound := crawlImages(pageSource)
	
	reg, err := regexp.Compile("[^a-z A-Z 0-9]")
	if err != nil {
		log.Fatal(err)
	}
	processedString := reg.ReplaceAllString(albumName, "")
	
	fmt.Println("Found", processedString, "set from", modelName, "!")
	fmt.Println("Found", len(imagesFound), "images in set. Downloading...")

	albumDir := downloadsDir + "/" + modelName + "/" + processedString

	checkAndCreateDir(downloadsDir)
	checkAndCreateDir(albumDir)
	imagesDownloaded := []string{}

	for i, imageURL := range imagesFound {
		imageOutput := albumDir + "/" + leftPad(strconv.Itoa(i), "0", digitsLen(len(imagesFound))-1) + ".jpg"
		fmt.Println(imageURL + " -> " + imageOutput)
		imagesDownloaded = append(imagesDownloaded, imageOutput)

		b, _ := saveImage(imageURL, imageOutput)
		fmt.Println("File size:", b)
	}

	if finalizeWithZip {
		err := ZipFiles(albumDir+"/"+albumName+".zip", imagesDownloaded)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Done... Enjoy!")
}
