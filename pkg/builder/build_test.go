package builder

import (
	"context"
	"io"
	"os"
	"path"
	"testing"

	"github.com/otiai10/copy"
	"github.com/taubyte/tau/core/builders"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"gotest.tools/v3/assert"
)

func TestBasicWebsite(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	repo, err := git.New(
		ctx,
		git.URL("https://github.com/taubyte-test/tb_static_template"),
		git.Temporary(),
	)
	assert.NilError(t, err)

	builder, err := New(ctx, repo.Dir())
	assert.NilError(t, err)

	output, err := builder.Build(builder.Wd().Website().SetWorkDir())
	assert.NilError(t, err)

	logs := output.Logs()
	assert.Assert(t, logs != nil, "output logs should not be nil")

	_, err = io.ReadAll(logs)
	assert.NilError(t, err)

	rsk, err := output.Compress(builders.Website)
	assert.NilError(t, err)
	assert.Assert(t, rsk != nil, "expected zipped website to not be nil")

	_, err = os.Stat(path.Join(repo.Dir(), "build.zip"))
	assert.NilError(t, err)
}

func TestWasmBasic(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	repo, err := git.New(
		ctx,
		git.URL("https://github.com/taubyte-test/tb_templates"),
		git.Temporary(),
		git.Preserve(),
	)
	assert.NilError(t, err)

	goTemplate := path.Join(repo.Dir(), "code", "functions", "Go")
	codeSource := path.Join(goTemplate, "empty")

	assert.NilError(t, copy.Copy(path.Join(goTemplate, "common"), codeSource))

	builder, err := New(ctx, codeSource)
	assert.NilError(t, err)

	output, err := builder.Build()
	assert.NilError(t, err)

	logs := output.Logs()
	assert.Assert(t, logs != nil, "output logs should not be nil")

	_, err = io.ReadAll(logs)
	assert.NilError(t, err)

	rsk, err := output.Compress(builders.WASM)
	assert.NilError(t, err)
	assert.Assert(t, rsk != nil, "output logs should not be nil")

	if _, err = os.Stat(wasm.WasmOutput(output.OutDir())); err != nil {
		_, err = os.Stat(wasm.WasmDeprecatedOutput(output.OutDir()))
	}
	assert.NilError(t, err)

	_, err = os.Stat(builder.Wd().Wasm().Zip())
	assert.NilError(t, err)
}
