package store

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/docker/cli/cli/manifest/fetcher"
	"github.com/docker/distribution/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRef struct {
	name string
}

func (f fakeRef) String() string {
	return f.name
}

func ref(name string) fakeRef {
	return fakeRef{name: name}
}

func newTestStore(t *testing.T) (Store, func()) {
	tmpdir, err := ioutil.TempDir("", "manifest-store-test")
	require.NoError(t, err)

	return NewStore(tmpdir), func() { os.RemoveAll(tmpdir) }
}

func getFiles(t *testing.T, store Store) []os.FileInfo {
	infos, err := ioutil.ReadDir(store.(*fsStore).root)
	require.NoError(t, err)
	return infos
}

func TestStoreRemove(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	listRef := ref("list")
	data := fetcher.ImgManifestInspect{RefName: "abcdef"}
	require.NoError(t, store.Save(listRef, ref("manifest"), data))
	require.Len(t, getFiles(t, store), 1)

	assert.NoError(t, store.Remove(listRef))
	assert.Len(t, getFiles(t, store), 0)
}

func TestStoreSaveAndGet(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	listRef := ref("list")
	data := fetcher.ImgManifestInspect{RefName: "abcdef"}
	require.NoError(t, store.Save(listRef, ref("exists"), data))

	var testcases = []struct {
		listRef     reference.Reference
		manifestRef reference.Reference
		expected    *fetcher.ImgManifestInspect
	}{
		{
			listRef:     listRef,
			manifestRef: ref("exists"),
			expected:    &data,
		},
		{
			listRef:     listRef,
			manifestRef: ref("does-not-exist"),
		},
		{
			listRef:     ref("list-does-not-exist"),
			manifestRef: ref("does-not-exist"),
		},
	}

	for _, testcase := range testcases {
		actual, err := store.Get(testcase.listRef, testcase.manifestRef)
		assert.NoError(t, err)
		assert.Equal(t, testcase.expected, actual)
	}
}

func TestStoreGetList(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	listRef := ref("list")
	first := fetcher.ImgManifestInspect{RefName: "first"}
	require.NoError(t, store.Save(listRef, ref("first"), first))
	second := fetcher.ImgManifestInspect{RefName: "second"}
	require.NoError(t, store.Save(listRef, ref("exists"), second))

	list, err := store.GetList(listRef)
	assert.NoError(t, err)
	assert.Len(t, list, 2)
}
