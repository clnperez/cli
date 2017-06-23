package manifest

import (
	"fmt"

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
