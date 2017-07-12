package manifest

import (
	"bytes"
//	"io"
	"io/ioutil"
//	"strings"
	"testing"

	"github.com/docker/cli/cli/internal/test"
	"github.com/docker/docker/pkg/testutil"
//	"github.com/stretchr/testify/assert"
)

func TestNewPushListCommand(t *testing.T) {
	testCases := []struct {
		name		string
		args		[]string
		expectedError	string
	}{
		{
			name:		"too-many-args",
			args:		[]string{"arg1", "arg2"},
			expectedError:	"Incorrect command format.",
		},
		{
			name:		"too-few-args",
			args:		[]string{},
			expectedError:	"Incorrect command format.",
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
/* TODO create and load in yaml here. Use golden?
func TestNewPushListSuccess(t *testing.T) {
	testCases := []struct{
		name		string
		args		[]string
	}{
		{
			name:	"simple-yaml",
			args:	[]string{"--file", "something.yaml"},
		},
	}
	for _, tc := range testCases {
		buf := new(bytes.Buffer)
		cli := test.NewFakeCli(&fakeClient{
			manifestPushListFunc: func(manifestList string, opts pushOptions)(io.ReadCloser, error){
				return ioutil.NopCloser(strings.NewReader("")), nil
			},
		}, buf)
		cmd := newPushListCommand(cli)
		cmd.SetOutput(ioutil.Discard)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		assert.NoError(t, err)
	}
}
*/
