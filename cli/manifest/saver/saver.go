package saver

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	//"io/ioutil"
	"os"
	"path/filepath"
	"regexp"

	climanifest "github.com/docker/cli/cli/manifest"
	"github.com/docker/distribution/manifest/manifestlist"
	digest "github.com/opencontainers/go-digest"
	ociv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func ManifestSaveFromArchives(outFile string, archives []string) error {
	fmt.Println("Save from archives: %s", archives)

	index := ociv1.Index{}
	platforms := make(map[string]manifestlist.PlatformSpec)

	// Loop through archives, untar, look in manifest.json to get platform info
	// & append to list.

	// @TODO: Don't pin this to pwd
	pwd, _ := os.Getwd()
	fmt.Println(pwd)

	for _, archive := range archives {
		img, err := getImage(filepath.Join(pwd, archive))
		if err != nil {
			return err
		}
		platform, err := getPlatform(img)
		if err != nil {
			return err
		}
		platforms[archive] = platform
		//fmt.Println(platform)
	}
	// then make manifest list spec json
	for archive, platform := range platforms {
		//fmt.Println("Key:", archive, "Value:", platform)
		d := digest.Digest(md.Digest)
	}
	// then make bundle with original tars and manifest list spec inside
	return nil
}

func getPlatform(img *climanifest.Image) (spec manifestlist.PlatformSpec, err error) {

	buff := make([]byte, 500)
	_, err = tr.Read(buff)
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(buff, img); err != nil {
		return spec, err
	}
	spec.Architecture = img.Architecture
	spec.OS = img.OS
	return spec, nil

}

func getImage(archive string) (*climanifest.Image, error) {
	var (
		img climanifest.Image
	)
	re, err := regexp.Compile("[0-9,a-f]{64}.json$")
	r, err := os.Open(archive)
	if err != nil {
		return spec, err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return spec, err
		}
		// Find the first config file: [hex-id].json
		if !re.MatchString(hdr.Name) {
			continue
		}
		buff := make([]byte, hdr.Size)
		_, err = tr.Read(buff)
		if err != nil {
			return img, err
		}
		if err := json.Unmarshal(buff, &img); err != nil {
			return spec, err
		}
	}
	return &img, nil
}

func ManifestSaveLocalImages(images []string) error {
	fmt.Println("Save from images: %s", images)
	return nil
}

func ManifestSaveFromRegistry(manifestList string) error {
	fmt.Println("Save %s from registry", manifestList)
	return nil
}
