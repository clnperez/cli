package fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/opencontainers/go-digest"
)

// RecoverableError signifies that other endpoints should be tried
type RecoverableError struct {
	original error
}

func (e RecoverableError) Error() string {
	return fmt.Sprintf("non-fatal fetch error: %s", e.original.Error())
}

// ImageConfigPullError is an error pulling the image config blob
// (only applies to schema2).
type ImageConfigPullError struct {
	Err error
}

// Error returns the error string for ImageConfigPullError.
func (e ImageConfigPullError) Error() string {
	return "error pulling image configuration: " + e.Err.Error()
}

// ImgManifestInspect contains info to output for a manifest object.
type ImgManifestInspect struct {
	RefName         string                 `json:"ref"`
	Size            int64                  `json:"size"`
	MediaType       string                 `json:"media_type"`
	Tag             string                 `json:"tag"`
	Digest          digest.Digest          `json:"digest"`
	RepoTags        []string               `json:"repotags"`
	Comment         string                 `json:"comment"`
	Created         string                 `json:"created"`
	ContainerConfig *containerTypes.Config `json:"container_config"`
	DockerVersion   string                 `json:"docker_version"`
	Author          string                 `json:"author"`
	Config          *containerTypes.Config `json:"config"`
	References      []string               `json:"references"`
	LayerDigests    []string               `json:"layers_digests"`
	Architecture    string                 `json:"architecture"`
	OS              string                 `json:"os"`
	OSVersion       string                 `json:"os.version,omitempty"`
	OSFeatures      []string               `json:"os.features,omitempty"`
	Variant         string                 `json:"variant,omitempty"`
	Features        []string               `json:"features,omitempty"`
	CanonicalJSON   []byte                 `json:"json"`
}

// The following mirror data structures from docker/docker/ to avoid importing all of distribtion/
// Types have been substituted where possible to keep imports to a minimum, but maintain the Image structure
// for use when unmarshaling docker/docker's image.Image JSON into a fetcher.Image.

// RootFS describes images root filesystem
type RootFS struct {
	// @TODO: consider moving the following to a more universal location (outside the manifest/fetcher pkg)
	Type    string          `json:"type"`
	DiffIDs []digest.Digest `json:"diff_ids,omitempty"`
}

// History stores build commands that were used to create an image
type History struct {
	// Created is the timestamp at which the image was created
	Created time.Time `json:"created"`
	// Author is the name of the author that was specified when committing the image
	Author string `json:"author,omitempty"`
	// CreatedBy keeps the Dockerfile command used while building the image
	CreatedBy string `json:"created_by,omitempty"`
	// Comment is the commit message that was set when committing the image
	Comment string `json:"comment,omitempty"`
	// EmptyLayer is set to true if this history item did not generate a
	// layer. Otherwise, the history item is associated with the next
	// layer in the RootFS section.
	EmptyLayer bool `json:"empty_layer,omitempty"`
}

// Image stores the image configuration
// It contains docker's v1Image fields for simplicity
type Image struct {
	// ID is a unique 64 character identifier of the image
	ID string `json:"id,omitempty"`
	// Parent is the ID of the parent image
	OldParent string `json:"oldparent,omitempty"`
	// Comment is the commit message that was set when committing the image
	Comment string `json:"comment,omitempty"`
	// Created is the timestamp at which the image was created
	Created time.Time `json:"created"`
	// Container is the id of the container used to commit
	Container string `json:"container,omitempty"`
	// ContainerConfig is the configuration of the container that is committed into the image
	ContainerConfig containerTypes.Config `json:"container_config,omitempty"`
	// DockerVersion specifies the version of Docker that was used to build the image
	DockerVersion string `json:"docker_version,omitempty"`
	// Author is the name of the author that was specified when committing the image
	Author string `json:"author,omitempty"`
	// Config is the configuration of the container received from the client
	Config *containerTypes.Config `json:"config,omitempty"`
	// Architecture is the hardware that the image is built and runs on
	Architecture string `json:"architecture,omitempty"`
	// OS is the operating system used to build and run the image
	OS string `json:"os,omitempty"`
	// Size is the total size of the image including all layers it is composed of
	Size       int64         `json:",omitempty"`
	Parent     digest.Digest `json:"parent,omitempty"`
	RootFS     *RootFS       `json:"rootfs,omitempty"`
	History    []History     `json:"history,omitempty"`
	OSVersion  string        `json:"os.version,omitempty"`
	OSFeatures []string      `json:"os.features,omitempty"`
}

// NewImageFromJSON creates an Image configuration from json.
func NewImageFromJSON(src []byte) (*Image, error) {
	img := &Image{}
	if err := json.Unmarshal(src, img); err != nil {
		return nil, err
	}
	return img, nil
}

type existingTokenHandler struct {
	token string
}

func (th *existingTokenHandler) AuthorizeRequest(req *http.Request, params map[string]string) error {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", th.token))
	return nil
}

func (th *existingTokenHandler) Scheme() string {
	return "bearer"
}
