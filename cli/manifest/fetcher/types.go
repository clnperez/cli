package fetcher

import (
	"fmt"
	"net/http"

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
