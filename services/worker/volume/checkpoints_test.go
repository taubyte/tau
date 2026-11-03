package volume_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/storage"
	"github.com/taubyte/tau/services/worker/volume"
)

// versioned is a storage service holding what was put, by root.
type versioned struct {
	at     storage.Address
	name   string
	object storage.Object
	held   map[string]string
}

func (v *versioned) Put(_ context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return storage.Version{}, err
	}
	v.at, v.name, v.object = at, name, o
	root := "bafy" + string(body)
	v.held[root] = string(body)
	return storage.Version{Name: name, Record: storage.VersionRecord{RootCID: root}}, nil
}

func (v *versioned) Read(_ context.Context, root string) (io.ReadCloser, error) {
	body, ok := v.held[root]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (*versioned) Current(context.Context, storage.Address, string) (storage.Version, error) {
	return storage.Version{}, errors.New("unused")
}
func (*versioned) Versions(context.Context, storage.Address, string, int) ([]storage.Version, error) {
	return nil, errors.New("unused")
}
func (*versioned) List(context.Context, storage.Address, string, string, int) ([]storage.Version, error) {
	return nil, errors.New("unused")
}
func (*versioned) Delete(context.Context, storage.Address, string) (storage.Version, error) {
	return storage.Version{}, errors.New("unused")
}

func TestACheckpointIsAVersionInTheVolumesOwnBucket(t *testing.T) {
	ctx := t.Context()
	store := &versioned{held: map[string]string{}}
	c := volume.Checkpoints(store)

	id, err := c.Put(ctx, "shop/prod/QmData", strings.NewReader("tar"))
	require.NoError(t, err)
	want, err := storage.NewAddress("shop", "prod", "QmData", "checkpoints")
	require.NoError(t, err)
	require.Equal(t, want, store.at)
	require.Equal(t, "checkpoint", store.name, "every checkpoint a version of one name")
	require.Equal(t, "application/x-tar", store.object.ContentType)

	r, err := c.Read(ctx, id)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "tar", string(body))

	_, err = c.Put(ctx, "no-handle", strings.NewReader("tar"))
	require.Error(t, err, "a handle that names no volume")
}
