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

type manifestInfo struct {
	blobDigests []digest.Digest
	layers      []string
	digest      digest.Digest
	platform    manifestlist.PlatformSpec
	length      int64
	jsonBytes   []byte
}

type manifestInfoAndImage struct {
	image        *Image
	manfiestInfo manifestInfo
}

// FetchOptions are the options required to fetch a image data from a registry
type FetchOptions struct {
	AuthConfig types.AuthConfig
	RepoInfo   *registry.RepositoryInfo
	Endpoint   registry.APIEndpoint
	NamedRef   reference.Named
}

// FetchManifest pulls a manifest from a registry and returns it. An error
// is returned if no manifest is found matching namedRef.
func FetchManifest(ctx context.Context, opts FetchOptions) (ImgManifestInspect, error) {
	repo, err := opts.getRepository(ctx)
	if err != nil {
		logrus.Debugf("error getting v2 registry: %v", err)
		return ImgManifestInspect{}, err
	}

	manifest, tagOrDigest, err := getManifest(ctx, repo, opts.NamedRef)
	if err != nil {
		return ImgManifestInspect{}, handleRecoverableError(err)
	}

	tagList, err := repo.Tags(ctx).All(ctx)
	if err != nil {
		return ImgManifestInspect{}, handleRecoverableError(err)
	}

	switch v := manifest.(type) {
	// Removed Schema 1 support
	case *schema2.DeserializedManifest:
		image, mfInfo, err := pullManifestSchemaV2(ctx, opts.NamedRef, repo, *v)
		if err != nil {
			return ImgManifestInspect{}, handleRecoverableError(err)
		}
		return makeImgManifestInspect(
			opts.NamedRef.String(), image, tagOrDigest, mfInfo, tagList), nil
	}
	return ImgManifestInspect{}, errors.Errorf("object at %s is not a manifest", opts.NamedRef)
}

// Fetch pulls a manifest or manifest list and return it
// TODO: remove duplication with FetchManifest
func Fetch(ctx context.Context, opts FetchOptions) ([]ImgManifestInspect, error) {
	repo, err := opts.getRepository(ctx)
	if err != nil {
		logrus.Debugf("error getting v2 registry: %v", err)
		return nil, err
	}

	manifest, tagOrDigest, err := getManifest(ctx, repo, opts.NamedRef)
	if err != nil {
		return nil, handleRecoverableError(err)
	}

	tagList, err := repo.Tags(ctx).All(ctx)
	if err != nil {
		return nil, handleRecoverableError(err)
	}

	switch v := manifest.(type) {
	// Removed Schema 1 support
	case *schema2.DeserializedManifest:
		image, mfInfo, err := pullManifestSchemaV2(ctx, opts.NamedRef, repo, *v)
		if err != nil {
			return nil, handleRecoverableError(err)
		}
		return []ImgManifestInspect{makeImgManifestInspect(
			opts.NamedRef.String(), image, tagOrDigest, mfInfo, tagList)}, nil
	case *manifestlist.DeserializedManifestList:
		infos, err := pullManifestList(ctx, opts.NamedRef, repo, *v)
		if err != nil {
			return nil, err
		}
		var imgManifests []ImgManifestInspect

		for _, info := range infos {
			imgManifest := makeImgManifestInspect(
				opts.NamedRef.String(), info.image, tagOrDigest, info.manfiestInfo, tagList)
			imgManifests = append(imgManifests, imgManifest)
		}
		return imgManifests, nil
	default:
		return nil, errors.Errorf("unsupported manifest format: %v", v)
	}
}

func handleRecoverableError(err error) error {
	if continueOnError(err) {
		return RecoverableError{original: err}
	}
	return err
}

// getManifest returns the manifest from a reference. Also returns the digest or
// tag of the reference.
func getManifest(ctx context.Context, repo distribution.Repository, ref reference.Named) (distribution.Manifest, string, error) {
	manSvc, err := repo.Manifests(ctx)
	if err != nil {
		return nil, "", err
	}

	if tagged, isTagged := ref.(reference.NamedTagged); isTagged {
		tag := tagged.Tag()
		manifest, err := manSvc.Get(ctx, "", distribution.WithTag(tag))
		return manifest, tag, err
	}
	if digested, isDigested := ref.(reference.Canonical); isDigested {
		manifest, err := manSvc.Get(ctx, digested.Digest())
		return manifest, digested.Digest().String(), err
	}

	return nil, "", errors.Errorf("image manifest for %q does not exist", ref)
}

func pullManifestSchemaV2(ctx context.Context, ref reference.Named, repo distribution.Repository, mfst schema2.DeserializedManifest) (*Image, manifestInfo, error) {
	mfDigest, err := schema2ManifestDigest(ref, mfst)
	if err != nil {
		return nil, manifestInfo{}, err
	}
	mfInfo := manifestInfo{digest: mfDigest}

	configJSON, err := pullManifestSchemaV2ImageConfig(ctx, mfst.Target().Digest, repo)
	if err != nil {
		return nil, mfInfo, err
	}

	img, err := NewImageFromJSON(configJSON)
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

func pullManifestSchemaV2ImageConfig(ctx context.Context, dgst digest.Digest, repo distribution.Repository) ([]byte, error) {
	blobs := repo.Blobs(ctx)
	configJSON, err := blobs.Get(ctx, dgst)
	if err != nil {
		return nil, err
	}

	verifier := dgst.Verifier()
	if err != nil {
		return nil, err
	}
	if _, err := verifier.Write(configJSON); err != nil {
		return nil, err
	}
	if !verifier.Verified() {
		return nil, errors.Errorf("image config verification failed for digest %s", dgst)
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
// platform-specific manifests.
func pullManifestList(ctx context.Context, ref reference.Named, repo distribution.Repository, mfstList manifestlist.DeserializedManifestList) ([]manifestInfoAndImage, error) {
	infos := []manifestInfoAndImage{}
	manifestListDigest, err := schema2ManifestDigest(ref, mfstList)
	if err != nil {
		return nil, err
	}
	logrus.Debugf("Pulling manifest list entries for ML digest %v", manifestListDigest)

	for _, manifestDescriptor := range mfstList.Manifests {

		manSvc, err := repo.Manifests(ctx)
		if err != nil {
			return nil, err
		}
		manifest, err := manSvc.Get(ctx, manifestDescriptor.Digest)
		if err != nil {
			return nil, err
		}
		v, ok := manifest.(*schema2.DeserializedManifest)
		if !ok {
			return nil, fmt.Errorf("unsupported manifest format: %s", v)
		}

		manifestRef, err := reference.WithDigest(ref, manifestDescriptor.Digest)
		if err != nil {
			return nil, err
		}
		img, mfInfo, err := pullManifestSchemaV2(ctx, manifestRef, repo, *v)
		if err != nil {
			return nil, err
		}
		mfInfo.platform = manifestDescriptor.Platform
		infos = append(infos, manifestInfoAndImage{image: img, manfiestInfo: mfInfo})
	}
	return infos, nil
}

func (opts FetchOptions) getRepository(ctx context.Context) (distribution.Repository, error) {
	if err := validateEndpoint(opts.Endpoint); err != nil {
		return nil, err
	}

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
	return client.NewRepository(ctx, repoNameRef, opts.Endpoint.URL.String(), tr)
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
	// TODO: this case seems like it would never get hit. Nothing returns this error
	case ImageConfigPullError:
		return false
	}
	return false
}

func makeImgManifestInspect(name string, img *Image, tag string, mfInfo manifestInfo, tagList []string) ImgManifestInspect {
	var digest digest.Digest
	if err := mfInfo.digest.Validate(); err == nil {
		digest = mfInfo.digest
	}

	var digests []string
	for _, blobDigest := range mfInfo.blobDigests {
		digests = append(digests, blobDigest.String())
	}
	return ImgManifestInspect{
		RefName:         name,
		Size:            mfInfo.length,
		MediaType:       schema2.MediaTypeManifest,
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
