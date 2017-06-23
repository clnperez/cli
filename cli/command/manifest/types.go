package manifest

import (
	"encoding/json"
	"fmt"
	"time"

	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/opencontainers/go-digest"
)

// recoverableError
type recoverableError struct {
	original error
}

func (e recoverableError) Error() string {
	return fmt.Sprintf("non-fatal fetch error: %e", e.original.Error())
}

// dirOpenError
type dirOpenError struct {
}

func (e dirOpenError) Error() string {
	return "cannot perform open on a directory"
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
	// The following are top-level objects because nested json from a file
	// won't unmarshal correctly.
	Architecture string   `json:"architecture"`
	OS           string   `json:"os"`
	OSVersion    string   `json:"os.version,omitempty"`
	OSFeatures   []string `json:"os.features,omitempty"`
	Variant      string   `json:"variant,omitempty"`
	Features     []string `json:"features,omitempty"`
	// This one's prettier at the end
	CanonicalJSON []byte `json:"json"`
}

// @TODO: Move the following to a more universal location (outside the manifest pkg)
type RootFS struct {
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

// Image mirrors the docker/docker/image Image type and should be kept in synch
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

	// rawJSON caches the immutable JSON associated with this image.
	rawJSON []byte

	// computedID is the ID computed from the hash of the image config.
	// Not to be confused with the legacy V1 ID in V1Image.
	computedID digest.Digest
}

// NewImgFromJSON creates an Image configuration from json.
func NewImageFromJSON(src []byte) (*Image, error) {
	img := &Image{}

	if err := json.Unmarshal(src, img); err != nil {
		return nil, err
	}

	img.rawJSON = src

	return img, nil
}
