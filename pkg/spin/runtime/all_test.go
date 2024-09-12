package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/go-sdk/utils/slices"
	"github.com/taubyte/tau/pkg/spin/embed"
	"gotest.tools/v3/assert"

	. "github.com/taubyte/tau/pkg/spin"

	_ "embed"
)

func TestNew(t *testing.T) {
	s, err := New(context.Background())
	assert.NilError(t, err)
	s.Close()
}

func TestNewContainer(t *testing.T) {
	for arch, opts := range map[string][]Option[Spin]{
		"amd64":   {Runtime[AMD64](nil)},
		"riscv64": {Runtime[RISCV64](nil)},
	} {
		t.Run("using "+arch, func(t *testing.T) {
			s, err := New(context.Background(), opts...)
			assert.NilError(t, err)

			var buf bytes.Buffer

			c, err := s.New(
				ImageFile(fmt.Sprintf("../fixtures/%s-hello-world.zip", arch)),
				Stdout(&buf), Stderr(&buf),
			)
			assert.NilError(t, err)
			defer s.Close()

			err = c.Run()
			assert.NilError(t, err)

			assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
		})
	}
}

func TestPreBuiltContainer(t *testing.T) {
	squashSrc, err := embed.ToolsSquashFS()
	assert.NilError(t, err)

	s, err := New(context.Background(), Module(squashSrc))
	assert.NilError(t, err)
	defer s.Close()

	var buf bytes.Buffer
	c, err := s.New(Command("uname", "-a"), Stdout(&buf), Stderr(&buf))
	assert.NilError(t, err)

	err = c.Run()
	assert.NilError(t, err)

	assert.Equal(t, strings.Contains(buf.String(), "riscv64 Linux"), true)
}

type mockReg struct{}

func (r *mockReg) Pull(ctx context.Context, image string, _ chan<- PullProgress) error {
	if !slices.Contains([]string{"riscv64/hello-world:latest", "amd64/hello-world:latest"}, image) {
		return errors.New("invalid image name")
	}
	return nil
}

func (r *mockReg) Path(image string) (string, error) {
	images := map[string]string{
		"riscv64/hello-world:latest": "../fixtures/riscv64-hello-world.zip",
		"amd64/hello-world:latest":   "../fixtures/amd64-hello-world.zip",
	}

	if p, ok := images[image]; ok {
		return p, nil
	}

	return "", os.ErrNotExist
}

func (r *mockReg) Close() {}

func TestPullContainerWithPrebuilt(t *testing.T) {
	squashSrc, err := embed.ToolsSquashFS()
	assert.NilError(t, err)

	s, err := New(context.Background(), Module(squashSrc))
	assert.NilError(t, err)
	defer s.Close()

	_, err = s.New(Image("amd64/hello-world:latest"))
	assert.Error(t, err, "only runtimes can use bundles")
}

func TestPullContainerWithoutRegistry(t *testing.T) {
	s, err := New(context.Background(), Runtime[AMD64](nil))
	assert.NilError(t, err)
	defer s.Close()

	_, err = s.New(Image("amd64/hello-world:latest"))
	assert.Error(t, err, "no registry")
}

func TestPullContainer(t *testing.T) {
	for arch, opts := range map[string][]Option[Spin]{
		"amd64":   {Runtime[AMD64](&mockReg{})},
		"riscv64": {Runtime[RISCV64](&mockReg{})},
	} {
		t.Run("using "+arch, func(t *testing.T) {
			s, err := New(context.Background(), opts...)
			assert.NilError(t, err)

			var buf bytes.Buffer

			c, err := s.New(
				Image(fmt.Sprintf("%s/hello-world:latest", arch)),
				Stdout(&buf), Stderr(&buf),
			)
			assert.NilError(t, err)
			defer s.Close()

			err = c.Run()
			assert.NilError(t, err)

			assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
		})
	}
}

func TestNetContainer(t *testing.T) {
	squashSrc, err := embed.ToolsSquashFS()
	assert.NilError(t, err)
	s, err := New(context.Background(), Module(squashSrc))
	assert.NilError(t, err)
	defer s.Close()

	var buf bytes.Buffer

	c, err := s.New(
		Stdout(&buf), Stderr(&buf),
		Networking(),
		Command("ping", "-c", "1", "8.8.8.8"),
	)
	assert.NilError(t, err)
	defer c.Stop()

	err = c.Run()
	assert.NilError(t, err)

	assert.Equal(t, strings.Contains(buf.String(), "bytes from 8.8.8.8: seq=0"), true)
}

func TestNetServiceContainer(t *testing.T) {
	s, err := New(context.Background(), ModuleZip("../fixtures/network-test-containers.zip", "network-test-container.wasm"))
	assert.NilError(t, err)
	defer s.Close()

	var buf bytes.Buffer

	c, err := s.New(
		Stdout(&buf), Stderr(&buf),
		Networking(Forward("18080", "8080")),
	)
	assert.NilError(t, err)
	defer c.Stop()

	go func() {
		err = c.Run()
		assert.NilError(t, err)
	}()

	ctx, ctxC := context.WithTimeout(context.TODO(), 20*time.Second)
	defer ctxC()

	var res *http.Response
tryLoop:
	for {
		select {
		case <-ctx.Done():
			t.Error("timeout")
		case <-time.After(1 * time.Second):
			res, err = http.Get("http://127.0.0.1:18080")
			if err == nil {
				defer res.Body.Close()
				break tryLoop
			}
		}
	}

	assert.NilError(t, err)

	resBody, err := io.ReadAll(res.Body)
	assert.NilError(t, err)

	assert.Equal(t, string(resBody), "Hello, World!")
}
