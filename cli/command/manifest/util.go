package manifest

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/docker/pkg/homedir"
)

type osArch struct {
	os   string
	arch string
}

// dirOpenError
type dirOpenError struct {
}

func (e dirOpenError) Error() string {
	return "cannot perform open on a directory"
}

// Remove any unsupported os/arch combo
// list of valid os/arch values (see "Optional Environment Variables" section
// of https://golang.org/doc/install/source
// Added linux/s390x as we know System z support already exists
var validOSArches = map[osArch]bool{
	{os: "darwin", arch: "386"}:      true,
	{os: "darwin", arch: "amd64"}:    true,
	{os: "darwin", arch: "arm"}:      true,
	{os: "darwin", arch: "arm64"}:    true,
	{os: "dragonfly", arch: "amd64"}: true,
	{os: "freebsd", arch: "386"}:     true,
	{os: "freebsd", arch: "amd64"}:   true,
	{os: "freebsd", arch: "arm"}:     true,
	{os: "linux", arch: "386"}:       true,
	{os: "linux", arch: "amd64"}:     true,
	{os: "linux", arch: "arm"}:       true,
	{os: "linux", arch: "arm64"}:     true,
	{os: "linux", arch: "ppc64le"}:   true,
	{os: "linux", arch: "mips64"}:    true,
	{os: "linux", arch: "mips64le"}:  true,
	{os: "linux", arch: "s390x"}:     true,
	{os: "netbsd", arch: "386"}:      true,
	{os: "netbsd", arch: "amd64"}:    true,
	{os: "netbsd", arch: "arm"}:      true,
	{os: "openbsd", arch: "386"}:     true,
	{os: "openbsd", arch: "amd64"}:   true,
	{os: "openbsd", arch: "arm"}:     true,
	{os: "plan9", arch: "386"}:       true,
	{os: "plan9", arch: "amd64"}:     true,
	{os: "solaris", arch: "amd64"}:   true,
	{os: "windows", arch: "386"}:     true,
	{os: "windows", arch: "amd64"}:   true,
}

func isValidOSArch(os string, arch string) bool {
	// check for existence of this combo
	_, ok := validOSArches[osArch{os, arch}]
	return ok
}

func makeFilesafeName(ref string) string {
	// Make sure the ref is a normalized name before calling this func
	fileName := strings.Replace(ref, ":", "-", -1)
	return strings.Replace(fileName, "/", "_", -1)
}

func getListFilenames(transaction string) ([]string, error) {
	baseDir, err := buildBaseFilename()
	if err != nil {
		return nil, err
	}
	transactionDir := filepath.Join(baseDir, makeFilesafeName(transaction))
	fd, err := os.Open(transactionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	fileNames, err := fd.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	fd.Close()
	for i, f := range fileNames {
		fileNames[i] = filepath.Join(transactionDir, f)
	}
	return fileNames, nil
}

func getManifestFd(manifest, transaction string) (*os.File, error) {

	fileName, err := mfToFilename(manifest, transaction)
	if err != nil {
		return nil, err
	}

	return getFdGeneric(fileName)
}

func getFdGeneric(file string) (*os.File, error) {
	fileinfo, err := os.Stat(file)
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if fileinfo.IsDir() {
		return nil, dirOpenError{}
	}
	fd, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	return fd, nil
}

func buildBaseFilename() (string, error) {
	// Get will check for $HOME and if not set, lookup a user in
	// a static-safe way (without calling os/user.Current())
	userHome := homedir.Get()
	return filepath.Join(userHome, ".docker", "manifests"), nil
}

func mfToFilename(manifest, transaction string) (string, error) {

	baseDir, err := buildBaseFilename()
	if err != nil {
		return "", nil
	}
	return filepath.Join(baseDir, makeFilesafeName(transaction), makeFilesafeName(manifest)), nil
}

func localManifestToManifestInspect(manifest, transaction string) (fetcher.ImgManifestInspect, error) {

	var newMI fetcher.ImgManifestInspect
	filename, err := mfToFilename(manifest, transaction)
	if err != nil {
		return newMI, err
	}
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return newMI, err
	}
	if err := json.Unmarshal(buf, &newMI); err != nil {
		return newMI, err
	}
	return newMI, nil
}

func updateMfFile(newMI fetcher.ImgManifestInspect, mfName, transaction string) error {
	fileName, err := mfToFilename(mfName, transaction)
	if err != nil {
		return err
	}
	if err := os.Remove(fileName); err != nil && !os.IsNotExist(err) {
		return err
	}
	fd, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	theBytes, err := json.Marshal(newMI)
	if err != nil {
		return err
	}

	if _, err := fd.Write(theBytes); err != nil {
		return err
	}
	return nil
}
