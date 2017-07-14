package fetcher

import (
	"fmt"
	"net"
	"net/http"
	"runtime"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/manifest/schema2"
	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/api/errcode"
	"github.com/docker/distribution/registry/api/v2"
	"github.com/docker/distribution/registry/client"
	"github.com/docker/distribution/registry/client/auth"
	"github.com/docker/distribution/registry/client/transport"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/dockerversion"
	"github.com/docker/docker/registry"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

// manifestFetcher is to retrieve manifest and image info for an image or manifest list
type manifestFetcher struct {
	repo distribution.Repository
}

type manifestInfo struct {
	blobDigests []digest.Digest
	layers      []string
	digest      digest.Digest
	platform    manifestlist.PlatformSpec
	length      int64
	jsonBytes   []byte
}

type manifestListInspect struct {
	imageInfos []*Image
	mfInfos    []manifestInfo
	mediaTypes []string
}

func validateEndpoint(endpoint registry.APIEndpoint) error {
	switch endpoint.Version {
	case registry.APIVersion2:
		return nil
	case registry.APIVersion1:
		return fmt.Errorf("v1 registries are no longer supported")
	}
	return fmt.Errorf("unknown version %d for registry %s", endpoint.Version, endpoint.URL)
}

// FetchOptions are the options required to fetch a image data from a registry
type FetchOptions struct {
	AuthConfig types.AuthConfig
	RepoInfo   *registry.RepositoryInfo
	Endpoint   registry.APIEndpoint
	NamedRef   reference.Named
}

// Fetch and return summarized image information or a manifest list
func Fetch(ctx context.Context, opts FetchOptions) ([]ImgManifestInspect, error) {
	if err := validateEndpoint(opts.Endpoint); err != nil {
		return nil, err
	}

	mf, err := opts.newFetcher(ctx)
	if err != nil {
		logrus.Debugf("Error getting v2 registry: %v", err)
		return nil, err
	}

	// Pre-condition: namedRef has to be tagged (e.g. using ParseNormalizedNamed)
	images, err := mf.fetchWithRepository(ctx, opts.NamedRef)
	if err != nil {
		if continueOnError(err) {
			return nil, RecoverableError{original: err}
		}
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
		listInspect, err := mf.pullManifestList(ctx, ref, *v)
		if err != nil {
			return nil, err
		}
		images = listInspect.imageInfos
		mfInfos = listInspect.mfInfos
		mediaType = listInspect.mediaTypes
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

	img, err = NewImageFromJSON(configJSON)
	if err != nil {
		return nil, mfInfo, err
	}
	if runtime.GOOS == "windows" {
		if img.RootFS == nil {
			return nil, mfInfo, errors.New("image config has no rootfs section")
		}
	}

	for _, descriptor := range mfst.References() {
		mfInfo.blobDigests = append(mfInfo.blobDigests, descriptor.Digest)
	}
	for _, layer := range mfst.Layers {
		mfInfo.layers = append(mfInfo.layers, layer.Digest.String())
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
		return nil, err
	}

	return configJSON, nil
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
			return "", err
		}
		return digested.Digest(), nil
	}

	return digest.FromBytes(canonical), nil
}

// pullManifestList handles "manifest lists" which point to various
// platform-specifc manifests.
func (mf *manifestFetcher) pullManifestList(ctx context.Context, ref reference.Named, mfstList manifestlist.DeserializedManifestList) (*manifestListInspect, error) {
	var (
		imageList = []*Image{}
		mfInfos   = []manifestInfo{}
		mediaType = []string{}
		v         *schema2.DeserializedManifest
		ok        bool
	)
	manifestListDigest, err := schema2ManifestDigest(ref, mfstList)
	if err != nil {
		return nil, err
	}
	logrus.Debugf("Pulling manifest list entries for ML digest %v", manifestListDigest)

	for _, manifestDescriptor := range mfstList.Manifests {
		manSvc, err := mf.repo.Manifests(ctx)
		if err != nil {
			return nil, err
		}

		manifest, err := manSvc.Get(ctx, manifestDescriptor.Digest)
		if err != nil {
			return nil, err
		}

		manifestRef, err := reference.WithDigest(ref, manifestDescriptor.Digest)
		if err != nil {
			return nil, err
		}

		if v, ok = manifest.(*schema2.DeserializedManifest); !ok {
			return nil, fmt.Errorf("unsupported manifest format: %s", v)
		}
		img, mfInfo, err := mf.pullSchema2(ctx, manifestRef, *v)
		if err != nil {
			return nil, err
		}
		imageList = append(imageList, img)
		mfInfo.platform = manifestDescriptor.Platform
		mfInfos = append(mfInfos, mfInfo)
		mediaType = append(mediaType, schema2.MediaTypeManifest)
		if err != nil {
			return nil, err
		}
	}

	return &manifestListInspect{imageInfos: imageList, mfInfos: mfInfos, mediaTypes: mediaType}, err
}

func (opts FetchOptions) newFetcher(ctx context.Context) (*manifestFetcher, error) {
	repoName := opts.RepoInfo.Name.Name()
	// If endpoint does not support CanonicalName, use the RemoteName instead
	if opts.Endpoint.TrimHostname {
		repoName = reference.Path(opts.RepoInfo.Name)
	}
	repoNameRef, err := reference.WithName(repoName)
	if err != nil {
		return nil, err
	}

	tr, err := GetDistClientTransport(opts.AuthConfig, opts.Endpoint, repoName)
	if err != nil {
		return nil, err
	}
	repo, err := client.NewRepository(ctx, repoNameRef, opts.Endpoint.URL.String(), tr)
	if err != nil {
		return nil, err
	}
	return &manifestFetcher{repo: repo}, nil
}

// GetDistClientTransport builds a transport for use in communicating with a registry
func GetDistClientTransport(authConfig types.AuthConfig, endpoint registry.APIEndpoint, repoName string) (http.RoundTripper, error) {
	// get the http transport, this will be used in a client to upload manifest
	base := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     endpoint.TLSConfig,
		DisableKeepAlives:   true,
	}

	modifiers := registry.DockerHeaders(dockerversion.DockerUserAgent(nil), http.Header{})
	authTransport := transport.NewTransport(base, modifiers...)
	challengeManager, confirmedV2, err := registry.PingV2Registry(endpoint.URL, authTransport)
	if err != nil {
		return nil, errors.Wrap(err, "error pinging v2 registry")
	}
	if !confirmedV2 {
		return nil, fmt.Errorf("unsupported registry version")
	}
	if authConfig.RegistryToken != "" {
		passThruTokenHandler := &existingTokenHandler{token: authConfig.RegistryToken}
		modifiers = append(modifiers, auth.NewAuthorizer(challengeManager, passThruTokenHandler))
	} else {
		creds := registry.NewStaticCredentialStore(&authConfig)
		tokenHandler := auth.NewTokenHandler(authTransport, creds, repoName, "*")
		basicHandler := auth.NewBasicHandler(creds)
		modifiers = append(modifiers, auth.NewAuthorizer(challengeManager, tokenHandler, basicHandler))
	}
	return transport.NewTransport(base, modifiers...), nil
}

func continueOnError(err error) bool {
	switch v := err.(type) {
	case errcode.Errors:
		if len(v) == 0 {
			return true
		}
		return continueOnError(v[0])
	case errcode.Error:
		e := err.(errcode.Error)
		switch e.Code {
		// @TODO: We should try remaning endpoints in these cases?
		case errcode.ErrorCodeUnauthorized, v2.ErrorCodeManifestUnknown, v2.ErrorCodeNameUnknown:
			return true
		}
		return false
	case *client.UnexpectedHTTPResponseError:
		return true
	case ImageConfigPullError:
		return false
	}
	// let's be nice and fallback if the error is a completely
	// unexpected one.
	// If new errors have to be handled in some way, please
	// add them to the switch above.
	return true
}

func makeImgManifestInspect(name string, img *Image, tag string, mfInfo manifestInfo, mediaType string, tagList []string) *ImgManifestInspect {
	var digest digest.Digest
	if err := mfInfo.digest.Validate(); err == nil {
		digest = mfInfo.digest
	}

	if mediaType == manifestlist.MediaTypeManifestList {
		return &ImgManifestInspect{
			MediaType: mediaType,
			Digest:    digest,
		}
	}

	var digests []string
	for _, blobDigest := range mfInfo.blobDigests {
		digests = append(digests, blobDigest.String())
	}
	return &ImgManifestInspect{
		RefName:         name,
		Size:            mfInfo.length,
		MediaType:       mediaType,
		Tag:             tag,
		Digest:          digest,
		RepoTags:        tagList,
		Comment:         img.Comment,
		Created:         img.Created.Format(time.RFC3339Nano),
		ContainerConfig: &img.ContainerConfig,
		DockerVersion:   img.DockerVersion,
		Author:          img.Author,
		Config:          img.Config,
		Architecture:    mfInfo.platform.Architecture,
		OS:              mfInfo.platform.OS,
		OSVersion:       mfInfo.platform.OSVersion,
		OSFeatures:      mfInfo.platform.OSFeatures,
		Variant:         mfInfo.platform.Variant,
		Features:        mfInfo.platform.Features,
		References:      digests,
		LayerDigests:    mfInfo.layers,
		CanonicalJSON:   mfInfo.jsonBytes,
	}
}
