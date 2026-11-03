package volume

import (
	"context"
	"fmt"
	"io"

	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/storage"
)

// checkpointInstance is the instance of a volume's bucket its checkpoints live in.
const checkpointInstance = "checkpoints"

// checkpointName is the one name a volume's checkpoints are versions of.
const checkpointName = "checkpoint"

// Checkpoints keeps a volume's checkpoints in the storage service, as versions of one
// name in the volume's own bucket.
func Checkpoints(store storage.Store) Assets { return checkpoints{store: store} }

type checkpoints struct{ store storage.Store }

func (c checkpoints) Put(ctx context.Context, handle string, content io.Reader) (string, error) {
	project, environment, id := specs.SplitHandle(handle)
	at, err := storage.NewAddress(project, environment, id, checkpointInstance)
	if err != nil {
		return "", fmt.Errorf("volume: %s: %w", handle, err)
	}
	v, err := c.store.Put(ctx, at, checkpointName, storage.Object{ContentType: "application/x-tar"}, content)
	if err != nil {
		return "", err
	}
	return v.Record.RootCID, nil
}

func (c checkpoints) Read(ctx context.Context, id string) (io.ReadCloser, error) {
	return c.store.Read(ctx, id)
}
