package manifest

import (
	"io"

	"github.com/docker/docker/client"
//	"golang.org/x/net/context"
)

type fakeClient struct {
	client.Client
	manifestAnnotateFunc	func(manifestList string, refImage string, opts annotateOptions)(io.ReadCloser, error)
	manifestPushListFunc	func(manifestList string, opts pushOptions)(io.ReadCloser, error)
	manifestInspectFunc	func(manifestList string, opts inspectOptions)(io.ReadCloser, error)


}

