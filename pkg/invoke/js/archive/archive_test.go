package archive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"

	"warpjs/engine"
)

func unpack(t *testing.T, data []byte) (*zip.Reader, map[string][]byte) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	out := map[string][]byte{}
	for _, f := range z.File {
		rc, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		out[f.Name] = body
	}
	return z, out
}

// TestAnArchiveCarriesItsManifestAndEveryFile, deflated, with the manifest
// first so a reader learns what it holds before anything else.
func TestAnArchiveCarriesItsManifestAndEveryFile(t *testing.T) {
	files := map[string][]byte{
		"worker.js":          []byte(`export default { fetch() {} }`),
		"chunks/a.js":        []byte(`export const a = 1`),
		"worker.js.map":      []byte(`{"version":3}`),
		"chunks/a.js.warp":   []byte("compiled"),
		"assets/readme.txt":  []byte("anything else the bundle needs"),
		"chunks/a.js.map":    []byte(`{}`),
		"worker.js.map.warp": []byte("a compiled form of any member"),
	}
	var buf bytes.Buffer
	require.NoError(t, Write(&buf, "worker.js", files))

	z, got := unpack(t, buf.Bytes())
	require.Equal(t, ManifestName, z.File[0].Name)
	for _, f := range z.File {
		require.Equal(t, zip.Deflate, f.Method, f.Name)
	}

	var m Manifest
	require.NoError(t, json.Unmarshal(got[ManifestName], &m))
	require.Equal(t, Manifest{Format: Format, Entry: "worker.js", Engine: Current()}, m)
	delete(got, ManifestName)
	require.Equal(t, files, got)
}

// TestOneBuildIsOneDigest: members are sorted and carry no timestamp, so
// packing the same files twice gives the same bytes.
func TestOneBuildIsOneDigest(t *testing.T) {
	files := map[string][]byte{"b.js": []byte("b"), "a.js": []byte("a"), "worker.js": []byte("w")}
	var one, two bytes.Buffer
	require.NoError(t, Write(&one, "worker.js", files))
	require.NoError(t, Write(&two, "worker.js", files))
	require.Equal(t, one.Bytes(), two.Bytes())

	z, _ := unpack(t, one.Bytes())
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	require.Equal(t, []string{ManifestName, "a.js", "b.js", "worker.js"}, names)
}

func TestWriteRefusesWhatAReaderCouldNotUse(t *testing.T) {
	cases := map[string]struct {
		entry string
		files map[string][]byte
		want  error
	}{
		"no entry":            {"worker.js", map[string][]byte{"other.js": nil}, ErrNoEntry},
		"a compiled entry":    {"worker.js.warp", map[string][]byte{"worker.js": nil, "worker.js.warp": nil}, ErrCompiledEntry},
		"an orphaned compile": {"worker.js", map[string][]byte{"worker.js": nil, "gone.js.warp": nil}, ErrOrphanCompile},
		"the manifest's name": {"worker.js", map[string][]byte{"worker.js": nil, ManifestName: nil}, ErrBadName},
		"an absolute name":    {"worker.js", map[string][]byte{"worker.js": nil, "/etc/x.js": nil}, ErrBadName},
		"a backslash":         {"worker.js", map[string][]byte{"worker.js": nil, `a\b.js`: nil}, ErrBadName},
		"an unclean name":     {"worker.js", map[string][]byte{"worker.js": nil, "a//b.js": nil}, ErrBadName},
		"a climbing name":     {"worker.js", map[string][]byte{"worker.js": nil, "../x.js": nil}, ErrBadName},
		"a parent name":       {"worker.js", map[string][]byte{"worker.js": nil, "..": nil}, ErrBadName},
		"an empty name":       {"", map[string][]byte{"": nil}, ErrBadName},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, Write(io.Discard, c.entry, c.files), c.want)
		})
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestAWriteThatFailsSaysSo(t *testing.T) {
	small := map[string][]byte{"worker.js": []byte("w")}
	require.ErrorContains(t, Write(failing{}, "worker.js", small), "disk full", "failing when the archive is finished")

	noise := make([]byte, 1<<20)
	random := rand.New(rand.NewPCG(1, 2))
	for i := range noise {
		noise[i] = byte(random.Uint32())
	}
	require.ErrorContains(t, Write(failing{}, "worker.js", map[string][]byte{"worker.js": noise}), "disk full",
		"failing while a member that does not compress is written")
}

// TestTheEngineIsTheOneThisBinaryLinks. A directory replace reports
// "(devel)", which is the reason a reader cannot trust it.
func TestTheEngineIsTheOneThisBinaryLinks(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	require.True(t, ok)
	got := Current()
	require.Equal(t, engine.BytecodeVersion(), got.Bytecode)
	require.Equal(t, versionIn(info.Deps), got.Warpjs)
	require.NotEmpty(t, got.Warpjs, "this binary links warpjs")
}

func TestTheVersionComesFromTheModuleThatIsBuilt(t *testing.T) {
	require.Empty(t, versionIn(nil))
	require.Empty(t, versionIn([]*debug.Module{{Path: "other", Version: "v1.0.0"}}))
	require.Equal(t, "v0.1.0", versionIn([]*debug.Module{{Path: modulePath, Version: "v0.1.0"}}))
	require.Equal(t, "(devel)", versionIn([]*debug.Module{{
		Path: modulePath, Version: "v0.0.0", Replace: &debug.Module{Path: "./.deps/warpjs", Version: "(devel)"},
	}}))
}
