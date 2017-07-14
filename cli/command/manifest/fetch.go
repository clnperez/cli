package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"golang.org/x/net/context"

	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/registry"
)

// TODO: a lot of this should be moved to the struct with storeManifest for
// managing local manifests
func getLocalImageManifestData(namedRef reference.Named, transactionID string) ([]fetcher.ImgManifestInspect, *registry.RepositoryInfo, error) {
	// TODO: extract as a function, duplicated in many places (Ex: annotate command)
	if transactionID != "" {
		transactionNamed, err := reference.ParseNormalizedNamed(transactionID)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "Error parsing reference for %s: %s", transactionID)
		}
		if _, isDigested := transactionNamed.(reference.Canonical); !isDigested {
			transactionNamed = reference.TagNameOnly(transactionNamed)
		}
		transactionID = makeFilesafeName(transactionNamed.String())
	}

	// Make sure these have a tag, as long as it's not a digest
	if _, isDigested := namedRef.(reference.Canonical); !isDigested {
		namedRef = reference.TagNameOnly(namedRef)
	}
	logrus.Debugf("getting image data for ref: %s", namedRef)

	// Resolve the Repository name from fqn to RepositoryInfo
	// This calls TrimNamed, which removes the tag, so always use namedRef for the image.
	repoInfo, err := registry.ParseRepositoryInfo(namedRef)
	if err != nil {
		return nil, nil, err
	}

	// If this is a manifest list, let's check for it locally so a user can see any modifications
	// he/she has made.
	logrus.Debugf("Checking locally for %s", namedRef)
	var foundImages []fetcher.ImgManifestInspect
	foundImages, err = loadManifest(makeFilesafeName(namedRef.String()), transactionID)
	if err != nil {
		return nil, nil, err
	}
	if len(foundImages) > 0 {
		return foundImages, repoInfo, nil
	}
	// For a manifest list request, the name should be used as the transactionID
	foundImages, err = loadManifestList(namedRef.String())
	if err != nil {
		return nil, nil, err
	}
	return foundImages, repoInfo, nil
}

func getImageData(dockerCli command.Cli, namedRef reference.Named, transactionID string, fetchOnly bool) ([]fetcher.ImgManifestInspect, *registry.RepositoryInfo, error) {
	foundImages, repoInfo, err := getLocalImageManifestData(namedRef, transactionID) // TODO:
	if err != nil || len(foundImages) > 0 {
		return foundImages, repoInfo, err
	}

	// TODO: this should be passed in
	ctx := context.Background()
	registryService := registry.NewService(registry.ServiceOptions{})

	// a list of registry.APIEndpoint, which could be mirrors, etc., of locally-configured
	// repo endpoints. The list will be ordered by priority (v2, https, v1).
	endpoints, err := registryService.LookupPullEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return nil, nil, err
	}
	logrus.Debugf("manifest pull: endpoints: %v", endpoints)

	authConfig := command.ResolveAuthConfig(ctx, dockerCli, repoInfo.Index)

	// Try to find the first endpoint that is *both* v2 and using TLS.
	confirmedTLSRegistries := make(map[string]bool)
	for _, endpoint := range endpoints {
		opts := fetcher.FetchOptions{
			AuthConfig: authConfig,
			Endpoint:   endpoint,
			RepoInfo:   repoInfo,
			NamedRef:   namedRef,
		}
		foundImages, err := fetchFromEndpoint(ctx, confirmedTLSRegistries, opts)
		if err != nil {
			return nil, nil, err
		}
		if len(foundImages) == 0 {
			continue
		}

		// TODO: handle case where there is more than 1 foundImages
		// TODO: Instead of a boolean fetchOnly pass in a localStore struct
		//  (the one that should be created from storeManifest and
		// getLocalImageManifestData). If the object != nil, then store
		if !fetchOnly {
			if err := storeManifest(foundImages[0], namedRef, transactionID); err != nil {
				logrus.Debugf("error storing manifest %s: %s", namedRef, err)
			}
		}
		return foundImages, repoInfo, nil
	}
	return nil, nil, fmt.Errorf("no endpoints found for %s", namedRef)
}

func fetchFromEndpoint(
	ctx context.Context,
	confirmedTLSRegistries map[string]bool,
	opts fetcher.FetchOptions,
) ([]fetcher.ImgManifestInspect, error) {
	endpoint := opts.Endpoint
	if endpoint.Version == registry.APIVersion1 {
		logrus.Debugf("Skipping v1 endpoint %s", endpoint.URL)
		return nil, nil
	}

	if endpoint.URL.Scheme != "https" {
		if _, confirmedTLS := confirmedTLSRegistries[endpoint.URL.Host]; confirmedTLS {
			logrus.Debugf("Skipping non-TLS endpoint %s for host/port that appears to use TLS", endpoint.URL)
			return nil, nil
		}
	}

	logrus.Debugf("Trying to fetch image manifest of %s repository from %s %s", opts.NamedRef, endpoint.URL, endpoint.Version)

	foundImages, err := fetcher.Fetch(ctx, opts)
	if err != nil {
		// Can a manifest fetch be cancelled? I don't think so...
		if _, ok := err.(fetcher.RecoverableError); ok {
			if endpoint.URL.Scheme == "https" {
				confirmedTLSRegistries[endpoint.URL.Host] = true
			}
			return nil, nil
		}
		logrus.Debugf("not continuing with fetch after unrecoverable error: %v", err)
		return nil, err
	}
	return foundImages, nil
}

func loadManifest(manifest string, transaction string) ([]fetcher.ImgManifestInspect, error) {
	// Load either a single manifest (if transaction is "", that's fine), or a
	// manifest list
	var foundImages []fetcher.ImgManifestInspect
	fd, err := getManifestFd(manifest, transaction)
	if err != nil {
		if _, dirOpen := err.(dirOpenError); !dirOpen {
			return nil, err
		}
	}
	if fd != nil {
		defer fd.Close()
		_, err := fd.Stat()
		if err != nil {
			return nil, err
		}
		mfInspect, err := localManifestToManifestInspect(manifest, transaction)
		if err != nil {
			return nil, err
		}
		foundImages = append(foundImages, mfInspect)
	}
	return foundImages, nil
}

func loadManifestList(transaction string) (foundImages []fetcher.ImgManifestInspect, _ error) {
	manifests, err := getListFilenames(transaction)
	if err != nil {
		return nil, err
	}
	for _, manifestFile := range manifests {
		fileParts := strings.Split(manifestFile, string(filepath.Separator))
		numParts := len(fileParts)
		mfInspect, err := localManifestToManifestInspect(fileParts[numParts-1], transaction)
		if err != nil {
			return nil, err
		}
		foundImages = append(foundImages, mfInspect)
	}
	return foundImages, nil
}

// TODO: some of this should be abstracted out to a struct responsible for
// managing the local manifests
func storeManifest(image fetcher.ImgManifestInspect, namedRef reference.Reference, transactionID string) error {
	if transactionID == "" {
		transactionID = namedRef.String()
	}
	transactionID = makeFilesafeName(transactionID)

	manifestBase, err := buildBaseFilename()
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(manifestBase, transactionID), 0755)
	name := makeFilesafeName(namedRef.String())
	logrus.Debugf("Storing  %s", name)

	return updateMfFile(image, name, transactionID)
}
