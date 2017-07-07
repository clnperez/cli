package manifest

import (
	"io"

	"github.com/docker/docker/client"
//	"golang.org/x/net/context"
)

type fakeClient struct {
	client.Client
	manifestAnnotateFunc	func(manifestList string, refImage string, opts annotateOptions)(io.ReadCloser, error)
}

