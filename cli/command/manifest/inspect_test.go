package manifest

import (
	"bytes"
	"io"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/docker/cli/cli/internal/test"
	"github.com/docker/docker/pkg/testutil"
	"github.com/stretchr/testify/assert"
)

func TestNewInspectCommand(t *testing.T) {
	testCases := []struct {
		name		string
		args		[]string
		expectedError	string
	}{
		{
			name:		"wrong-args",
			args:		[]string{},
			expectedError:	"\"annotate\" requires exactly 1 argument(s).",
		},
	}
	for _, tc := range testCases {
		buf := new(bytes.Buffer)
		cmd := newInspectCommand(test.NewFakeCli(&fakeClient{}, buf))
		cmd.SetOutput(ioutil.Discard)
		cmd.SetArgs(tc.args)
		testutil.ErrorContains(t, cmd.Execute(), tc.expectedError)

	}
}

func TestNewInspectCreateSuccess(t *testing.T) {
	testCases := []struct{
		name		string
		args		[]string
	}{
		{
			name:	"simple",
			args:	[]string{"image"},
		},
		{
			name:	"manifest"
			args:	[]string{"trollin/alpine"}
	}
	for _, tc := range testCases {
		buf := new(bytes.Buffer)
		cmd := newAnnotateCommand(test.NewFakeCli(&fakeClient{
			manifestAnnotateFunc: func(manifestList string, refImage string, opts annotateOptions)(io.ReadCloser, error){
				return ioutil.NopCloser(strings.NewReader("")), nil
			},
		}, buf))
		cmd.SetOutput(ioutil.Discard)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		assert.NoError(t, err)
	}
}
