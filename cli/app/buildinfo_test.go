package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestBuildInfo(t *testing.T) {
	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	debugInfo = func() (info *debug.BuildInfo, ok bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.22.0",
			Path:      "github.com/taubyte/tau",
			Main: debug.Module{
				Path: "github.com/taubyte/tau",
			},
			Deps: []*debug.Module{
				{
					Path:    "atomicgo.dev/cursor",
					Version: "v0.2.0",
					Sum:     "h1:H6XN5alUJ52FZZUkI7AlJbUc1aW38GWZalpYRPpoPOw=",
				},
				{
					Path:    "atomicgo.dev/keyboard",
					Version: "v0.2.9",
					Sum:     "h1:tOsIid3nlPLZ3lwgG8KZMp/SFmr7P0ssEN5JUsm78K8=",
				},
			},
			Settings: []debug.BuildSetting{
				{
					Key:   "-buildmode",
					Value: "exe",
				},
				{
					Key:   "-compiler",
					Value: "gc",
				},
				{
					Key:   "GOARCH",
					Value: "amd64",
				},
				{
					Key:   "GOOS",
					Value: "linux",
				},
				{
					Key:   "vcs",
					Value: "git",
				},
				{
					Key:   "vcs.revision",
					Value: "92752b7bae67ab78d1de8b6ee4a3af8c7fdbb3cd",
				},
			},
		}, true
	}

	var (
		fakeOutput     bytes.Buffer
		fakeOutputLock sync.Mutex
	)
	buildInfoOutput = &fakeOutput

	t.Run("info build", func(t *testing.T) {
		fakeOutputLock.Lock()
		defer fakeOutputLock.Unlock()

		fakeOutput.Reset()

		err := newApp().RunContext(ctx, []string{os.Args[0], "info", "build"})

		assert.NilError(t, err)

		assert.Equal(t, fakeOutput.String(), `go	go1.22.0
path	github.com/taubyte/tau
mod	github.com/taubyte/tau		
build	-buildmode=exe
build	-compiler=gc
build	GOARCH=amd64
build	GOOS=linux
build	vcs=git
build	vcs.revision=92752b7bae67ab78d1de8b6ee4a3af8c7fdbb3cd

`)
	})

	t.Run("info build --deps", func(t *testing.T) {
		fakeOutputLock.Lock()
		defer fakeOutputLock.Unlock()

		fakeOutput.Reset()

		err := newApp().RunContext(ctx, []string{os.Args[0], "info", "build", "--deps"})

		assert.NilError(t, err)

		assert.Equal(t, fakeOutput.String(), `go	go1.22.0
path	github.com/taubyte/tau
mod	github.com/taubyte/tau		
dep	atomicgo.dev/cursor	v0.2.0	h1:H6XN5alUJ52FZZUkI7AlJbUc1aW38GWZalpYRPpoPOw=
dep	atomicgo.dev/keyboard	v0.2.9	h1:tOsIid3nlPLZ3lwgG8KZMp/SFmr7P0ssEN5JUsm78K8=
build	-buildmode=exe
build	-compiler=gc
build	GOARCH=amd64
build	GOOS=linux
build	vcs=git
build	vcs.revision=92752b7bae67ab78d1de8b6ee4a3af8c7fdbb3cd

`)
	})

	t.Run("info build --json", func(t *testing.T) {
		fakeOutputLock.Lock()
		defer fakeOutputLock.Unlock()

		fakeOutput.Reset()
		err := newApp().RunContext(ctx, []string{os.Args[0], "info", "build", "--json"})

		assert.NilError(t, err)

		fout := fakeOutput.Bytes()

		// Define a struct to unmarshal the JSON output
		type BuildSetting struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		}

		type Module struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
			Sum     string `json:"Sum"`
			Replace any    `json:"Replace"`
		}

		type BuildInfoOutput struct {
			GoVersion string         `json:"GoVersion"`
			Path      string         `json:"Path"`
			Main      Module         `json:"Main"`
			Deps      []*Module      `json:"Deps"`
			Settings  []BuildSetting `json:"Settings"`
		}

		var output BuildInfoOutput
		err = json.Unmarshal(fout, &output)
		assert.NilError(t, err)

		// Compare specific fields instead of the entire string
		assert.Equal(t, output.GoVersion, "go1.22.0")
		assert.Equal(t, output.Path, "github.com/taubyte/tau")
		assert.Equal(t, output.Main.Path, "github.com/taubyte/tau")
		assert.Equal(t, output.Main.Version, "")
		assert.Equal(t, output.Main.Sum, "")
		assert.Equal(t, output.Main.Replace, nil)

		// Verify specific build settings
		assert.Equal(t, len(output.Settings), 6)

		// Check for specific build settings
		foundBuildMode := false
		foundCompiler := false
		foundGOARCH := false
		foundGOOS := false
		foundVCS := false
		foundRevision := false

		for _, setting := range output.Settings {
			switch setting.Key {
			case "-buildmode":
				assert.Equal(t, setting.Value, "exe")
				foundBuildMode = true
			case "-compiler":
				assert.Equal(t, setting.Value, "gc")
				foundCompiler = true
			case "GOARCH":
				assert.Equal(t, setting.Value, "amd64")
				foundGOARCH = true
			case "GOOS":
				assert.Equal(t, setting.Value, "linux")
				foundGOOS = true
			case "vcs":
				assert.Equal(t, setting.Value, "git")
				foundVCS = true
			case "vcs.revision":
				assert.Equal(t, setting.Value, "92752b7bae67ab78d1de8b6ee4a3af8c7fdbb3cd")
				foundRevision = true
			}
		}

		// Ensure all expected settings were found
		assert.Equal(t, foundBuildMode, true, "build mode setting not found")
		assert.Equal(t, foundCompiler, true, "compiler setting not found")
		assert.Equal(t, foundGOARCH, true, "GOARCH setting not found")
		assert.Equal(t, foundGOOS, true, "GOOS setting not found")
		assert.Equal(t, foundVCS, true, "vcs setting not found")
		assert.Equal(t, foundRevision, true, "vcs revision setting not found")
	})

	t.Run("info commit", func(t *testing.T) {
		fakeOutputLock.Lock()
		defer fakeOutputLock.Unlock()

		fakeOutput.Reset()
		err := newApp().RunContext(ctx, []string{os.Args[0], "info", "commit"})

		assert.NilError(t, err)

		fout := fakeOutput.Bytes()

		assert.Equal(t, string(fout), `92752b7bae67ab78d1de8b6ee4a3af8c7fdbb3cd
`)

	})

}

func TestBuildInfoFail(t *testing.T) {
	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	debugInfo = func() (info *debug.BuildInfo, ok bool) {
		return nil, false
	}
	var fakeOutput bytes.Buffer
	buildInfoOutput = &fakeOutput

	err := newApp().RunContext(ctx, []string{os.Args[0], "info", "build"})

	assert.Error(t, err, "no build information found")

}
