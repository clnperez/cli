package manifest

import (
	"testing"

	"fmt"

	"github.com/docker/cli/internal/test"
	"github.com/gotestyourself/gotestyourself/golden"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnotateManifest(t *testing.T) {
	store, _ := newTempManifestStore(t)
	//defer cleanup()

	cli := test.NewFakeCli(nil)
	cli.SetManifestStore(store)
	namedRef := ref(t, "alpine:3.0")
	imageManifest := fullImageManifest(t, namedRef)
	err := store.Save(ref(t, "list:v1"), namedRef, imageManifest)
	require.NoError(t, err)

	cmd := newAnnotateCommand(cli)
	cmd.SetArgs([]string{"example.com/list:v1", "example.com/alpine:3.0"})
	err = cmd.Flags().Set("variant", "v7")
	require.NoError(t, err)
	require.NoError(t, cmd.Execute())

	fmt.Println(cmd.Flags())
	cmd = newInspectCommand(cli)
	err = cmd.Flags().Set("verbose", "true")
	require.NoError(t, err)
	cmd.SetArgs([]string{"example.com/list:v1", "example.com/alpine:3.0"})
	require.NoError(t, cmd.Execute())
	actual := cli.OutBuffer()
	expected := golden.Get(t, "inspect-annotate.golden")
	assert.Equal(t, string(expected), actual.String())
}
