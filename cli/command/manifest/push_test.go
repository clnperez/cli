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

func TestNewPushListCommand(t *testing.T) {
	testCases := []struct {
		name		string
		args		[]string
		expectedError	string
	}{
		{
			name:		"wrong-args",
			args:		[]string{"arg1", "arg2"},
			expectedError:	"\"push\" requires less than 1(s).",
		},
	}
	for _, tc := range testCases {
		buf := new(bytes.Buffer)
		cmd := newPushListCommand(test.NewFakeCli(&fakeClient{}, buf))
		cmd.SetOutput(ioutil.Discard)
		cmd.SetArgs(tc.args)
		testutil.ErrorContains(t, cmd.Execute(), tc.expectedError)

	}
}

func TestNewPushListSuccess(t *testing.T) {
	testCases := []struct{
		name		string
		args		[]string
	}{
		{
			name:	"simple-yaml",
			args:	[]string{"image:tag", "busybox"},
		},
	}
	for _, tc := range testCases {
		buf := new(bytes.Buffer)
		cmd := newPushListCommand(test.NewFakeCli(&fakeClient{
			manifestPushListFunc: func(manifestList string, refImage string, opts annotateOptions)(io.ReadCloser, error){
				return ioutil.NopCloser(strings.NewReader("")), nil
			},
		}, buf))
		cmd.SetOutput(ioutil.Discard)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		assert.NoError(t, err)
	}
}
