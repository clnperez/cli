package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/net/context"

	"github.com/Sirupsen/logrus"
	"github.com/docker/cli/cli/command"
	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/manifest/schema2"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/registry"
	digest "github.com/opencontainers/go-digest"
)

type manifestFetcher struct {
	endpoint    registry.APIEndpoint
	repoInfo    *registry.RepositoryInfo
	repo        distribution.Repository
	confirmedV2 bool
	authConfig  types.AuthConfig
	// Leaving this as a pointer to an interface won't compile for me
	service registry.Service
}

type manifestInfo struct {
	blobDigests []digest.Digest
	layers      []string
	digest      digest.Digest
	platform    manifestlist.PlatformSpec
	length      int64
	jsonBytes   []byte
}

func (mf *manifestFetcher) Fetch(ctx context.Context, dockerCli command.Cli, ref reference.Named) ([]ImgManifestInspect, error) {
	// Pre-condition: ref has to be tagged (e.g. using ParseNormalizedNamed)
	var err error

	mf.repo, mf.confirmedV2, err = newV2Repository(ctx, dockerCli, mf.repoInfo, mf.endpoint, nil, &mf.authConfig, "pull")
	if err != nil {
		logrus.Debugf("Error getting v2 registry: %v", err)
		return nil, err
	}

	images, err := mf.fetchWithRepository(ctx, ref)
	if err != nil {
		if continueOnError(err) {
			return nil, recoverableError{original: err}
		}
		logrus.Errorf("Error trying registry: %v", err)
		return nil, err
	}
	for _, img := range images {
		img.MediaType = schema2.MediaTypeManifest
	}
	return images, err
}

func (mf *manifestFetcher) fetchWithRepository(ctx context.Context, ref reference.Named) ([]ImgManifestInspect, error) {
	var (
		manifest    distribution.Manifest
		tagOrDigest string // Used for logging/progress only
		tagList     []string
		imageList   []ImgManifestInspect
	)

	manSvc, err := mf.repo.Manifests(ctx)
	if err != nil {
		return nil, err
	}

	if tagged, isTagged := ref.(reference.NamedTagged); isTagged {
		manifest, err = manSvc.Get(ctx, "", distribution.WithTag(tagged.Tag()))
		if err != nil {
			return nil, err
		}
		tagOrDigest = tagged.Tag()
	} else if digested, isDigested := ref.(reference.Canonical); isDigested {
		manifest, err = manSvc.Get(ctx, digested.Digest())
		if err != nil {
			return nil, err
		}
		tagOrDigest = digested.Digest().String()
	} else {
		return nil, fmt.Errorf("internal error: reference has neither a tag nor a digest: %s", ref.String())
	}

	if manifest == nil {
		return nil, fmt.Errorf("image manifest does not exist for tag or digest %q", tagOrDigest)
	}

	// If manSvc.Get succeeded, we can be confident that the registry on
	// the other side speaks the v2 protocol.
	mf.confirmedV2 = true

	tagList, err = mf.repo.Tags(ctx).All(ctx)
	if err != nil {
		return nil, err
	}

	var (
		images    []*Image
		mfInfos   []manifestInfo
		mediaType []string
	)

	switch v := manifest.(type) {
	// Removed Schema 1 support
	case *schema2.DeserializedManifest:
		image, mfInfo, err := mf.pullSchema2(ctx, ref, *v)
		images = append(images, image)
		mfInfos = append(mfInfos, mfInfo)
		mediaType = append(mediaType, schema2.MediaTypeManifest)
		if err != nil {
			return nil, err
		}
	case *manifestlist.DeserializedManifestList:
		images, mfInfos, mediaType, err = mf.pullManifestList(ctx, ref, *v)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported manifest format: %v", v)
	}

	for idx, img := range images {
		imgReturn := makeImgManifestInspect(ref.String(), img, tagOrDigest, mfInfos[idx], mediaType[idx], tagList)
		imageList = append(imageList, *imgReturn)
	}
	return imageList, nil
}

func (mf *manifestFetcher) pullSchema2(ctx context.Context, ref reference.Named, mfst schema2.DeserializedManifest) (*Image, manifestInfo, error) {
	var (
		img *Image
	)

	mfDigest, err := schema2ManifestDigest(ref, mfst)
	if err != nil {
		return nil, manifestInfo{}, err
	}
	mfInfo := manifestInfo{
		digest: mfDigest}

	// Pull the image config
	configJSON, err := mf.pullSchema2ImageConfig(ctx, mfst.Target().Digest)
	if err != nil {
		return nil, mfInfo, err
	}

	unmarshalledConfig, err := unmarshalConfig(configJSON)
	if err != nil {
		return nil, mfInfo, err
	}
	if runtime.GOOS == "windows" {
		if unmarshalledConfig.RootFS == nil {
			return nil, mfInfo, errors.New("image config has no rootfs section")
		}
	}

	for _, descriptor := range mfst.References() {
		mfInfo.blobDigests = append(mfInfo.blobDigests, descriptor.Digest)
	}
	for _, layer := range mfst.Layers {
		mfInfo.layers = append(mfInfo.layers, layer.Digest.String())
	}

	img, err = NewImageFromJSON(configJSON)
	if err != nil {
		return nil, mfInfo, err
	}
	// add the size of the manifest to the image response; needed for assembling proper
	// manifest lists
	_, mfBytes, err := mfst.Payload()
	if err != nil {
		return nil, mfInfo, err
	}
	mfInfo.length = int64(len(mfBytes))
	mfInfo.jsonBytes = mfBytes
	mfInfo.platform = manifestlist.PlatformSpec{
		OS:           img.OS,
		Architecture: img.Architecture,
		OSVersion:    img.OSVersion,
		OSFeatures:   img.OSFeatures,
	}
	return img, mfInfo, nil
}

func (mf *manifestFetcher) pullSchema2ImageConfig(ctx context.Context, dgst digest.Digest) ([]byte, error) {
	blobs := mf.repo.Blobs(ctx)
	configJSON, err := blobs.Get(ctx, dgst)
	if err != nil {
		return nil, err
	}

	// Verify image config digest
	verifier := dgst.Verifier()
	if err != nil {
		return nil, err
	}
	if _, err := verifier.Write(configJSON); err != nil {
		return nil, err
	}
	if !verifier.Verified() {
		err := fmt.Errorf("image config verification failed for digest %s", dgst)
		logrus.Error(err)
		return nil, err
	}

	return configJSON, nil
}

func unmarshalConfig(configJSON []byte) (Image, error) {
	var unmarshalledConfig Image
	if err := json.Unmarshal(configJSON, &unmarshalledConfig); err != nil {
		return Image{}, err
	}
	return unmarshalledConfig, nil
}

// schema2ManifestDigest computes the manifest digest, and, if pulling by
// digest, ensures that it matches the requested digest.
func schema2ManifestDigest(ref reference.Named, mfst distribution.Manifest) (digest.Digest, error) {
	_, canonical, err := mfst.Payload()
	if err != nil {
		return "", err
	}

	// If pull by digest, then verify the manifest digest.
	if digested, isDigested := ref.(reference.Canonical); isDigested {
		verifier := digested.Digest().Verifier()
		if err != nil {
			return "", err
		}
		if _, err := verifier.Write(canonical); err != nil {
			return "", err
		}
		if !verifier.Verified() {
			err := fmt.Errorf("manifest verification failed for digest %s", digested.Digest())
			logrus.Error(err)
			return "", err
		}
		return digested.Digest(), nil
	}

	return digest.FromBytes(canonical), nil
}

// pullManifestList handles "manifest lists" which point to various
// platform-specifc manifests.
func (mf *manifestFetcher) pullManifestList(ctx context.Context, ref reference.Named, mfstList manifestlist.DeserializedManifestList) ([]*Image, []manifestInfo, []string, error) {
	var (
		imageList = []*Image{}
		mfInfos   = []manifestInfo{}
		mediaType = []string{}
		v         *schema2.DeserializedManifest
		ok        bool
	)
	manifestListDigest, err := schema2ManifestDigest(ref, mfstList)
	if err != nil {
		return nil, nil, nil, err
	}
	logrus.Debugf("Pulling manifest list entries for ML digest %v", manifestListDigest)

	for _, manifestDescriptor := range mfstList.Manifests {
		manSvc, err := mf.repo.Manifests(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		thisDigest := manifestDescriptor.Digest
		thisPlatform := manifestDescriptor.Platform
		manifest, err := manSvc.Get(ctx, thisDigest)
		if err != nil {
			return nil, nil, nil, err
		}

		manifestRef, err := reference.WithDigest(ref, thisDigest)
		if err != nil {
			return nil, nil, nil, err
		}

		if v, ok = manifest.(*schema2.DeserializedManifest); !ok {
			return nil, nil, nil, fmt.Errorf("unsupported manifest format: %s v")
		}
		img, mfInfo, err := mf.pullSchema2(ctx, manifestRef, *v)
		imageList = append(imageList, img)
		mfInfo.platform = thisPlatform
		mfInfos = append(mfInfos, mfInfo)
		mediaType = append(mediaType, schema2.MediaTypeManifest)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	return imageList, mfInfos, mediaType, err
}
