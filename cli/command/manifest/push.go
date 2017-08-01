package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/api/v2"
	"github.com/docker/distribution/registry/client"
	digest "github.com/opencontainers/go-digest"

	//"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/docker/pkg/homedir"
	"github.com/docker/docker/registry"
)

type pushOpts struct {
	newRef string
	file   string
	purge  bool
}

// YamlInput represents the YAML format input to the pushml
// command.
type YamlInput struct {
	Image     string
	Manifests []YamlManifestEntry
}

// YamlManifestEntry represents an entry in the list of manifests to
// be combined into a manifest list, provided via the YAML input
type YamlManifestEntry struct {
	Image    string
	Platform manifestlist.PlatformSpec
}

// we will store up a list of blobs we must ask the registry
// to cross-mount into our target namespace
type blobMount struct {
	FromRepo reference.Named
	Digest   digest.Digest
}

// if we have mounted blobs referenced from manifests from
// outside the target repository namespace we will need to
// push them to our target's repo as they will be references
// from the final manifest list object we push
type manifestPush struct {
	Name      string
	Digest    string
	JSONBytes []byte
	MediaType string
}

type manifestListPush struct {
	targetRepoInfo    *registry.RepositoryInfo
	targetRef         reference.Named
	targetEndpoint    registry.APIEndpoint
	targetName        string
	list              manifestlist.ManifestList
	mountRequests     []manifestPush
	blobMountRequests []blobMount
}

func newPushListCommand(dockerCli command.Cli) *cobra.Command {

	opts := pushOpts{}

	cmd := &cobra.Command{
		Use:   "push [newRef | --file pre-annotated-yaml] [--purge=false]",
		Short: "Push a manifest list for an image to a repository",
		Args:  checkArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return putManifestList(dockerCli, opts, args)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&opts.file, "file", "f", "", "Path to a file containing a manifest list and its annotated constituent manifests")
	flags.BoolVarP(&opts.purge, "purge", "p", true, "After pushing, delete the user's locally-stored manifest list info")

	return cmd
}

func putManifestList(dockerCli command.Cli, opts pushOpts, args []string) error {
	var (
		yamlInput                         YamlInput
		initialRef                        string
		listPush                          manifestListPush
		fullTargetRef, targetRef, bareRef reference.Named
		err                               error
	)

	if opts.file != "" {
		yamlInput, err = getYamlInput(opts.file)
		if err != nil {
			return err
		}
		initialRef = yamlInput.Image
	} else {
		initialRef = args[0]
	}

	fullTargetRef, targetRef, bareRef, err = constructTargetRefs(initialRef)
	if err != nil {
		return err
	}
	logrus.Infof("beginning push of manifests into %s", fullTargetRef.String())

	targetRepoInfo, err := registry.ParseRepositoryInfo(fullTargetRef)
	if err != nil {
		return errors.Wrapf(err, "error parsing repository name for manifest list (%s): %v", opts.newRef)
	}
	targetEndpoint, targetRepoName, err := setupRepo(targetRepoInfo)
	if err != nil {
		return errors.Wrapf(err, "error setting up repository endpoint and references for %q: %v", targetRef)
	}
	logrus.Debugf("creating target ref: %s", fullTargetRef.String())

	ctx := context.Background()

	// Now create the manifest list payload by looking up the manifest schemas
	// for the constituent images:
	logrus.Debugf("retrieving digests of images...")
	if opts.file == "" {
		listPush, err = listFromTransaction(targetRepoInfo, targetRepoName, fullTargetRef.String())
	} else {
		listPush, err = listFromYAML(dockerCli, fullTargetRef, targetRepoInfo, targetRepoName, yamlInput)
	}
	if err != nil {
		return err
	}

	listPush.targetRepoInfo = targetRepoInfo
	listPush.targetName = targetRepoName
	listPush.targetRef = targetRef
	listPush.targetEndpoint = targetEndpoint

	err = doListPush(ctx, dockerCli, listPush, bareRef)
	if err != nil {
		return err
	}
	if opts.purge {
		targetFilename, _ := mfToFilename(fullTargetRef.String(), "")
		logrus.Debugf("deleting files at %s", targetFilename)
		if err := os.RemoveAll(targetFilename); err != nil {
			// Not a fatal error
			logrus.Info("unable to clean up manifest files in %s", targetFilename)
		}
	}
	return nil
}

func doListPush(ctx context.Context, dockerCli command.Cli, listPush manifestListPush, bareRef reference.Named) error {

	targetURL := listPush.targetEndpoint.URL.String()
	// Set the schema version
	listPush.list.Versioned = manifestlist.SchemaVersion

	urlBuilder, err := v2.NewURLBuilderFromString(targetURL, false)
	logrus.Debugf("manifest: put: target endpoint url: %s", targetURL)
	if err != nil {
		return errors.Wrapf(err, "can't create URL builder from endpoint (%s): %v", targetURL)
	}
	pushURL, err := createManifestURLFromRef(listPush.targetRef, urlBuilder)
	if err != nil {
		return errors.Wrapf(err, "error setting up repository endpoint and references for %q: %v", listPush.targetRef)
	}
	logrus.Debugf("manifest list push url: %s", pushURL)

	deserializedManifestList, err := manifestlist.FromDescriptors(listPush.list.Manifests)
	if err != nil {
		return errors.Wrap(err, "cannot deserialize manifest list")
	}
	mediaType, p, err := deserializedManifestList.Payload()
	logrus.Debugf("mediaType of manifestList: %s", mediaType)
	if err != nil {
		return errors.Wrap(err, "cannot retrieve payload for HTTP PUT of manifest list")

	}
	putRequest, err := http.NewRequest("PUT", pushURL, bytes.NewReader(p))
	if err != nil {
		return errors.Wrap(err, "HTTP PUT request creation failed")
	}
	putRequest.Header.Set("Content-Type", mediaType)

	tr, err := fetcher.GetDistClientTransport(ctx, dockerCli, listPush.targetRepoInfo, listPush.targetEndpoint, listPush.targetName)
	if err != nil {
		return errors.Wrap(err, "failed to setup HTTP client to repository")
	}
	httpClient := &http.Client{Transport: tr}

	// before we push the manifest list, if we have any blob mount requests, we need
	// to ask the registry to mount those blobs in our target so they are available
	// as references
	if err := mountBlobs(ctx, httpClient, targetURL, listPush.targetRef, listPush.blobMountRequests); err != nil {
		return errors.Wrap(err, "couldn't mount blobs for cross-repository push")
	}

	// we also must push any manifests that are referenced in the manifest list into
	// the target namespace
	// Use the untagged target for this so the digest is used
	// *could* i use targetRef instead of bareRef??
	if err := pushReferences(httpClient, urlBuilder, bareRef, listPush.mountRequests); err != nil {
		return errors.Wrap(err, "couldn't push manifests referenced in our manifest list")
	}

	resp, err := httpClient.Do(putRequest)
	if err != nil {
		return errors.Wrap(err, "v2 registry PUT of manifest list failed")
	}
	defer resp.Body.Close()

	if statusSuccess(resp.StatusCode) {
		dgstHeader := resp.Header.Get("Docker-Content-Digest")
		dgst, err := digest.Parse(dgstHeader)
		if err != nil {
			return err
		}
		logrus.Infof("successfully pushed manifest list %s with digest %s", listPush.targetRef, dgst)
		return nil
	}
	return fmt.Errorf("registry push unsuccessful: response %d: %s", resp.StatusCode, resp.Status)
}

func listFromTransaction(targetRepoInfo *registry.RepositoryInfo, targetRepoName, targetRef string) (manifestListPush, error) {
	var (
		manifestList      manifestlist.ManifestList
		blobMountRequests []blobMount
		manifestRequests  []manifestPush
		listPush          manifestListPush
		manifests         []string
		err               error
	)
	if manifests, err = getListFilenames(targetRef); err != nil {
		return listPush, err
	}
	// @TODO: Is this possible, or will manifests just be nil??
	if len(manifests) == 0 {
		return listPush, fmt.Errorf("%s not found", targetRef)
	}
	// manifests is a list of file paths
	for _, manifestFile := range manifests {
		fileParts := strings.Split(manifestFile, string(filepath.Separator))
		numParts := len(fileParts)
		mfstInspect, err := localManifestToManifestInspect(fileParts[numParts-1], fileParts[numParts-2])
		if err != nil {
			return listPush, err
		}
		if mfstInspect.Architecture == "" || mfstInspect.OS == "" {
			return listPush, fmt.Errorf("malformed manifest object. cannot push to registry")
		}
		// @TODO: Why am I getting another repoInfo here?
		manifest, repoInfo, err := buildManifestObj(targetRepoInfo, mfstInspect)
		if err != nil {
			return listPush, err
		}
		manifestList.Manifests = append(manifestList.Manifests, manifest)

		// if this image is in a different repo, we need to add the layer/blob digests to the list of
		// requested blob mounts (cross-repository push) before pushing the manifest list
		// @TODO: Test pushing manifest list where targetRepoName == manifestRepoName for all manifests
		manifestRepoName := reference.Path(repoInfo.Name)
		if targetRepoName != manifestRepoName {
			bmr, mr := buildBlobMountRequestLists(mfstInspect, targetRepoInfo.Name, repoInfo.Name)
			blobMountRequests = append(blobMountRequests, bmr...)
			manifestRequests = append(manifestRequests, mr...)
		}
	}
	listPush.mountRequests = manifestRequests
	listPush.blobMountRequests = blobMountRequests
	listPush.list = manifestList

	return listPush, nil
}

func listFromYAML(dockerCli command.Cli, targetRef reference.Named, targetRepoInfo *registry.RepositoryInfo, targetRepoName string, yamlInput YamlInput) (manifestListPush, error) {
	var (
		manifestList      manifestlist.ManifestList
		blobMountRequests []blobMount
		manifestRequests  []manifestPush
		listPush          manifestListPush
	)
	for _, mfEntry := range yamlInput.Manifests {
		mfstInspects, repoInfo, err := getImageData(dockerCli, mfEntry.Image, targetRef.Name(), true)
		if err != nil {
			return listPush, err
		}
		if len(mfstInspects) == 0 {
			return listPush, fmt.Errorf("manifest %s not found", mfEntry.Image)
		}
		mfstInspect := mfstInspects[0]
		if mfstInspect.Architecture == "" || mfstInspect.OS == "" {
			return listPush, fmt.Errorf("malformed manifest object. cannot push to registry")
		}
		manifest, repoInfo, err := buildManifestObj(targetRepoInfo, mfstInspect)
		if err != nil {
			return listPush, err
		}
		manifestList.Manifests = append(manifestList.Manifests, manifest)

		// if this image is in a different repo, we need to add the layer/blob digests to the list of
		// requested blob mounts (cross-repository push) before pushing the manifest list
		manifestRepoName := reference.Path(repoInfo.Name)
		if targetRepoName != manifestRepoName {
			bmr, mr := buildBlobMountRequestLists(mfstInspect, targetRepoInfo.Name, repoInfo.Name)
			blobMountRequests = append(blobMountRequests, bmr...)
			manifestRequests = append(manifestRequests, mr...)

		}
	}
	listPush.mountRequests = manifestRequests
	listPush.blobMountRequests = blobMountRequests
	listPush.list = manifestList
	return listPush, nil
}

func constructTargetRefs(initialRef string) (reference.Named, reference.Named, reference.Named, error) {
	var (
		targetRefNoDomain reference.Named
		targetRefNoTag    reference.Named
		fullTargetRef     reference.Named
		err               error
	)

	fullTargetRef, err = reference.ParseNormalizedNamed(initialRef)
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "error parsing name for manifest list (%s): %v")
	}
	if _, isDigested := fullTargetRef.(reference.Canonical); !isDigested {
		fullTargetRef = reference.TagNameOnly(fullTargetRef)
	}
	tagIndex := strings.LastIndex(fullTargetRef.String(), ":")
	logrus.Debugf("fullTargetRef. should be complete by now: %s", fullTargetRef.String())
	if tagIndex < 0 {
		return nil, nil, nil, fmt.Errorf("malformed reference")
	}
	tag := fullTargetRef.String()[tagIndex+1:]
	targetRefNoTag, err = reference.WithName(reference.Path(fullTargetRef))
	logrus.Debugf("targetRefNoTag should have no name and no tag: %s", targetRefNoTag.String())
	if err != nil {
		return nil, nil, nil, err
	}
	targetRefNoDomain, _ = reference.WithTag(targetRefNoTag, tag)
	logrus.Debugf("targetRefNoDomain should have no domain but a tag? %s", targetRefNoDomain.String())

	return fullTargetRef, targetRefNoDomain, targetRefNoTag, nil
}

func getYamlInput(yamlFile string) (YamlInput, error) {
	logrus.Debugf("YAML file: %s", yamlFile)

	if _, err := os.Stat(yamlFile); err != nil {
		logrus.Debugf("unable to open file: %s", yamlFile)
	}

	var yamlInput YamlInput
	yamlBuf, err := ioutil.ReadFile(yamlFile)
	if err != nil {
		return YamlInput{}, errors.Wrapf(err, "can't read YAML file %s", yamlFile)
	}
	if err = yaml.Unmarshal(yamlBuf, &yamlInput); err != nil {
		return YamlInput{}, errors.Wrapf(err, "can't unmarshal YAML file %s", yamlFile)
	}
	return yamlInput, nil
}

func buildManifestObj(targetRepo *registry.RepositoryInfo, mfInspect fetcher.ImgManifestInspect) (manifestlist.ManifestDescriptor, *registry.RepositoryInfo, error) {

	manifestRef, err := reference.ParseNormalizedNamed(mfInspect.RefName)
	if err != nil {
		return manifestlist.ManifestDescriptor{}, nil, err
	}
	repoInfo, err := registry.ParseRepositoryInfo(manifestRef)
	if err != nil {
		return manifestlist.ManifestDescriptor{}, nil, err
	}

	manifestRepoHostname := reference.Domain(repoInfo.Name)
	targetRepoHostname := reference.Domain(targetRepo.Name)
	if manifestRepoHostname != targetRepoHostname {
		return manifestlist.ManifestDescriptor{}, nil, fmt.Errorf("cannot use source images from a different registry than the target image: %s != %s", manifestRepoHostname, targetRepoHostname)
	}

	manifest := manifestlist.ManifestDescriptor{
		Platform: manifestlist.PlatformSpec{
			Architecture: mfInspect.Architecture,
			OS:           mfInspect.OS,
			OSVersion:    mfInspect.OSVersion,
			OSFeatures:   mfInspect.OSFeatures,
			Variant:      mfInspect.Variant,
			Features:     mfInspect.Features,
		},
	}
	manifest.Descriptor.Digest = mfInspect.Digest
	manifest.Size = mfInspect.Size
	manifest.MediaType = mfInspect.MediaType

	err = manifest.Descriptor.Digest.Validate()
	if err != nil {
		return manifestlist.ManifestDescriptor{}, nil, errors.Wrapf(err, "Digest parse of image %q failed with error: %v", manifestRef)
	}

	return manifest, repoInfo, nil
}

func buildBlobMountRequestLists(mfstInspect fetcher.ImgManifestInspect, targetRepoName, mfRepoName reference.Named) ([]blobMount, []manifestPush) {

	var (
		blobMountRequests []blobMount
		manifestRequests  []manifestPush
	)

	logrus.Debugf("adding manifest references of %q to blob mount requests to %s", mfRepoName, targetRepoName)
	for _, layer := range mfstInspect.References {
		dgst, _ := digest.Parse(layer)
		blobMountRequests = append(blobMountRequests, blobMount{FromRepo: targetRepoName, Digest: dgst})
	}
	// also must add the manifest to be pushed in the target namespace
	logrus.Debugf("adding manifest %q -> to be pushed to %q as a manifest reference", mfRepoName, targetRepoName)
	manifestRequests = append(manifestRequests, manifestPush{
		Name:      mfRepoName.String(),
		Digest:    mfstInspect.Digest.String(),
		JSONBytes: mfstInspect.CanonicalJSON,
		MediaType: mfstInspect.MediaType,
	})
	return blobMountRequests, manifestRequests
}

func createManifestURLFromRef(targetRef reference.Named, urlBuilder *v2.URLBuilder) (string, error) {

	manifestURL, err := urlBuilder.BuildManifestURL(targetRef)
	if err != nil {
		return "", errors.Wrap(err, "failed to build manifest URL from target reference")
	}
	return manifestURL, nil
}

func setupRepo(repoInfo *registry.RepositoryInfo) (registry.APIEndpoint, string, error) {
	endpoint, err := selectPushEndpoint(repoInfo)
	if err != nil {
		return endpoint, "", err
	}
	repoName := repoInfo.Name.Name()
	// If endpoint does not support CanonicalName, use the RemoteName instead
	if endpoint.TrimHostname {
		repoName = reference.Path(repoInfo.Name)
	}
	return endpoint, repoName, nil
}

func selectPushEndpoint(repoInfo *registry.RepositoryInfo) (registry.APIEndpoint, error) {
	var err error

	options := registry.ServiceOptions{}
	// By default (unless deprecated), loopback (IPv4 at least...) is automatically added as an insecure registry.
	options.InsecureRegistries, err = loadLocalInsecureRegistries()
	if err != nil {
		return registry.APIEndpoint{}, err
	}
	registryService := registry.NewService(options)
	endpoints, err := registryService.LookupPushEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return registry.APIEndpoint{}, err
	}
	// Default to the highest priority endpoint to return
	endpoint := endpoints[0]
	if !repoInfo.Index.Secure {
		for _, ep := range endpoints {
			if ep.URL.Scheme == "http" {
				endpoint = ep
			}
		}
	}
	return endpoint, nil
}

func loadLocalInsecureRegistries() ([]string, error) {
	insecureRegistries := []string{}
	// Check $HOME/.docker/config.json. There may be mismatches between what the user has in their
	// local config and what the daemon they're talking to allows, but we can be okay with that.
	userHome, err := homedir.GetStatic()
	if err != nil {
		return []string{}, fmt.Errorf("manifest create: lookup local insecure registries: Unable to retrieve $HOME")
	}

	jsonData, err := ioutil.ReadFile(filepath.Join(userHome, ".docker/config.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			return []string{}, errors.Wrap(err, "manifest create:")
		}
		// If the file just doesn't exist, no insecure registries were specified.
		logrus.Debug("manifest: no insecure registries were specified via $HOME/.docker/config.json")
		return []string{}, nil
	}

	if jsonData != nil {
		cf := configfile.ConfigFile{}
		if err := json.Unmarshal(jsonData, &cf); err != nil {
			logrus.Debugf("manifest create: unable to unmarshal insecure registries from $HOME/.docker/config.json: %s", err)
			return []string{}, nil
		}
		if cf.InsecureRegistries == nil {
			return []string{}, nil
		}
		// @TODO: Add tests for a) specifying in config.json, b) invalid entries
		for _, reg := range cf.InsecureRegistries {
			if err := net.ParseIP(reg); err == nil {
				insecureRegistries = append(insecureRegistries, reg)
			} else if _, _, err := net.ParseCIDR(reg); err == nil {
				insecureRegistries = append(insecureRegistries, reg)
			} else if ips, err := net.LookupHost(reg); err == nil {
				insecureRegistries = append(insecureRegistries, ips...)
			} else {
				return []string{}, errors.Wrapf(err, "manifest create: Invalid registry (%s) specified in ~/.docker/config.json: %s", reg)
			}
		}
	}

	return insecureRegistries, nil
}

func pushReferences(httpClient *http.Client, urlBuilder *v2.URLBuilder, ref reference.Named, manifests []manifestPush) error {
	for _, manifest := range manifests {
		dgst, err := digest.Parse(manifest.Digest)
		if err != nil {
			return errors.Wrapf(err, "error parsing manifest digest (%s) for referenced manifest %q: %v", manifest.Digest, manifest.Name)
		}
		targetRef, err := reference.WithDigest(ref, dgst)
		if err != nil {
			return errors.Wrapf(err, "error creating manifest digest target for referenced manifest %q: %v", manifest.Name)
		}
		pushURL, err := urlBuilder.BuildManifestURL(targetRef)
		if err != nil {
			return errors.Wrapf(err, "error setting up manifest push URL for manifest references for %q: %v", manifest.Name)
		}

		pushRequest, err := http.NewRequest("PUT", pushURL, bytes.NewReader(manifest.JSONBytes))
		if err != nil {
			return errors.Wrap(err, "HTTP PUT request creation for manifest reference push failed")
		}
		pushRequest.Header.Set("Content-Type", manifest.MediaType)
		resp, err := httpClient.Do(pushRequest)
		if err != nil {
			return errors.Wrap(err, "PUT of manifest reference failed")
		}

		resp.Body.Close()
		if !statusSuccess(resp.StatusCode) {
			return fmt.Errorf("referenced manifest push unsuccessful: response %d: %s", resp.StatusCode, resp.Status)
		}
		dgstHeader := resp.Header.Get("Docker-Content-Digest")
		dgstResult, err := digest.Parse(dgstHeader)
		if err != nil {
			return errors.Wrap(err, "couldn't parse pushed manifest digest response")
		}
		logrus.Infof("pushed manifest (%s) digest:  %s", manifest.Name, string(dgstResult))
	}
	return nil
}

func mountBlobs(ctx context.Context, httpClient *http.Client, targetURL string, ref reference.Named, blobsRequested []blobMount) error {
	for _, blob := range blobsRequested {
		repo, err := client.NewRepository(ctx, ref, targetURL, httpClient.Transport)
		if err != nil {
			return err
		}
		bs := repo.Blobs(ctx)
		fromCanonical, err := reference.WithDigest(blob.FromRepo, blob.Digest)
		if err != nil {
			return err
		}
		lu, err := bs.Create(ctx, client.WithMountFrom(fromCanonical))
		if err != nil {
			if _, ok := err.(distribution.ErrBlobMounted); ok {
				// mount successful
			}
		} else {
			// registry treated this as a normal upload
			lu.Cancel(ctx)
		}
		logrus.Debugf("mount of blob %s succeeded", blob.Digest.String())
	}
	return nil
}

func checkArgs(cmd *cobra.Command, args []string) error {
	useErr := fmt.Errorf("Incorrect command format.\n Usage: %s", cmd.Use)
	numArgs := len(args)
	if numArgs > 1 {
		return useErr
	}
	fileFlag := cmd.Flags().Lookup("file")
	if fileFlag.Changed && numArgs != 0 {
		return useErr
	}
	if !fileFlag.Changed && numArgs == 0 {
		return useErr
	}
	purgeFlag := cmd.Flags().Lookup("purge")
	if purgeFlag.Changed && fileFlag.Changed {
		return fmt.Errorf("using '--purge' doesn't make sense with '--file'")
	}
	return nil
}

func statusSuccess(status int) bool {
	return status >= 200 && status <= 399
}
