package containers

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/containers/core"
)

// localImage exists locally and cannot be pulled: a registry outage, or a name
// that is only ever a local tag.
type localImage struct {
	core.Image
	pulls, builds int
}

func (i *localImage) Exists(context.Context) bool { return true }
func (i *localImage) Pull(context.Context) error  { i.pulls++; return errors.New("registry down") }
func (i *localImage) Build(context.Context, *core.DockerfileBuild) error {
	i.builds++
	return nil
}

type localBackend struct {
	fakeBackend
	image *localImage
}

func (b *localBackend) Image(string) core.Image { return b.image }

// A failed pull falls back to a local image only if this process pulled it: any
// other local image may be another tenant's build, named on purpose.
func TestImageFallbackOnlyToWhatThisNodePulled(t *testing.T) {
	ctx := context.Background()
	backend := &localBackend{image: &localImage{}}
	client := &Client{backend: backend}

	name := "taubyte/go-wasi-0123abcd"
	defer lastPulled.Delete(name)
	_, err := client.Image(ctx, name, Output(&bytes.Buffer{}))
	assert.Error(t, err, "a local image this node never pulled was used")

	_, err = client.Image(ctx, name, Output(&bytes.Buffer{}), TrustLocal())
	assert.NoError(t, err, "a single-tenant node trusts its own images")

	markPulled(name)
	_, err = client.Image(ctx, name, Output(&bytes.Buffer{}))
	assert.NoError(t, err, "an image this node pulled outlives a registry outage")

	// A Dockerfile build's own image is reused, without asking a registry for it.
	backend.image = &localImage{}
	_, err = client.Image(ctx, "built-feedbeef", Output(&bytes.Buffer{}), Build(&bytes.Buffer{}))
	assert.NoError(t, err)
	assert.Zero(t, backend.image.pulls)
	assert.Zero(t, backend.image.builds)
}

var _ core.Image = (*localImage)(nil)
