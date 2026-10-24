package js_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/js/archive"
)

const hello = `export default { fetch(request) { return new Response("hello " + request.method); } };`

// TestBothModesServe: every engine mode runs the same handler, and the tier
// says which one it runs.
func TestBothModesServe(t *testing.T) {
	modes := []js.Mode{js.ModeBytecode}
	if engine.NativeSupported() {
		modes = append(modes, js.ModeNative, js.ModeJIT)
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t, hello, js.WithMode(mode))
			require.Equal(t, mode, f.tier.Mode())
			require.Equal(t, "hello GET", string(serve(t, f.start(), get("/")).Body))
		})
	}
}

func TestTheDefaultModeIsTheFastestTheArchHas(t *testing.T) {
	want := js.ModeBytecode
	if engine.NativeSupported() {
		want = js.ModeNative
	}
	require.Equal(t, want, js.DefaultMode())
	tier, err := js.New(&store{})
	require.NoError(t, err)
	require.Equal(t, want, tier.Mode())
	require.Equal(t, js.Name, tier.Name())
}

func TestNewRefusesWhatItCannotUse(t *testing.T) {
	_, err := js.New(nil)
	require.ErrorIs(t, err, js.ErrNoAssets)

	_, err = js.New(&store{}, js.WithMode("turbo"))
	require.ErrorIs(t, err, js.ErrUnknownMode)

	_, err = js.New(&store{}, js.WithMaxArchive(0))
	require.ErrorIs(t, err, js.ErrNoRoom)
}

// TestStartRefusesBeforeFetchingAnything what this tier cannot run.
func TestStartRefusesBeforeFetchingAnything(t *testing.T) {
	f := newFixture(t, hello)
	cases := map[string]struct {
		adjust func(*invoke.Spec)
		want   error
	}{
		"another tier's spec": {func(s *invoke.Spec) { s.Runtime = "wasm" }, invoke.ErrNoRuntime},
		"no artifact":         {func(s *invoke.Spec) { s.Artifact = "" }, js.ErrNoArtifact},
		"another world": {func(s *invoke.Spec) {
			s.Settings = map[string]string{js.SettingImplements: "gateway-router"}
		}, js.ErrUnsupportedWorld},
		"a mount": {func(s *invoke.Spec) {
			s.Reaches = []invoke.Grant{{Kind: invoke.MountKind, Label: "data"}}
		}, js.ErrNoMounts},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			spec := f.spec
			c.adjust(&spec)
			_, err := f.tier.Start(t.Context(), spec)
			require.ErrorIs(t, err, c.want)
		})
	}

	spec := f.spec
	spec.Settings = map[string]string{js.SettingImplements: js.WorldHandler}
	inst, err := f.tier.Start(t.Context(), spec)
	require.NoError(t, err, "the handler world, said out loud, is this tier's")
	require.NoError(t, inst.Close())
}

func TestStartSaysWhenTheAssetCannotBeHad(t *testing.T) {
	f := newFixture(t, hello)
	f.store.err = errors.New("no provider for that cid")
	_, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorContains(t, err, "no provider for that cid")
	require.ErrorContains(t, err, f.spec.Name)
}

type broken struct{}

func (broken) Read([]byte) (int, error)       { return 0, errors.New("the disk went away") }
func (broken) Seek(int64, int) (int64, error) { return 0, nil }
func (broken) Close() error                   { return nil }

type readerStore struct{ r io.ReadSeekCloser }

func (s readerStore) Get(context.Context, string) (io.ReadSeekCloser, error) { return s.r, nil }

func TestStartSaysWhenTheAssetCannotBeRead(t *testing.T) {
	tier, err := js.New(readerStore{broken{}})
	require.NoError(t, err)
	_, err = tier.Start(t.Context(), invoke.Spec{Name: "x", Revision: "r1", Artifact: "asset"})
	require.ErrorContains(t, err, "the disk went away")
}

// TestUnservedGrantsAreSaidOutLoud: no capability reaches JS yet, so a
// function that declared one is told its env is empty, kind by kind.
func TestUnservedGrantsAreSaidOutLoud(t *testing.T) {
	var said []string
	f := newFixture(t, hello, js.WithWarn(func(format string, args ...any) {
		said = append(said, format)
		for _, a := range args {
			said = append(said, a.(string))
		}
	}))
	f.spec.Reaches = []invoke.Grant{{Kind: "storage", Label: "files"}, {Kind: "cache", Label: "hot"}, {Kind: "cache", Label: "cold"}}
	f.start()
	require.Len(t, said, 3)
	require.Contains(t, said[0], "no host interface for yet")
	require.Equal(t, []string{f.spec.Name, "cache, storage"}, said[1:])
}

// TestAnEntryWithoutAHandlerIsRefusedByName at Start, rather than failing on
// the first request.
func TestAnEntryWithoutAHandlerIsRefusedByName(t *testing.T) {
	cases := map[string]struct {
		worker string
		call   string
		want   string
	}{
		"no default export":       {`export const fetch = () => new Response("x");`, "", "the entry has no default export"},
		"a default that is 42":    {`export default 42;`, "", "its default export is not an object"},
		"a default with no fetch": {`export default { handle() {} };`, "", "its default export has no fetch method"},
		"a named export missing":  {hello, "api", `the entry has no export "api"`},
		"a fetch getter that throws": {
			`export default { get fetch() { throw new Error("no fetch for you"); } };`, "", "no fetch for you",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, c.worker)
			if c.call != "" {
				f.spec.Settings = map[string]string{js.SettingCall: c.call}
			}
			_, err := f.tier.Start(t.Context(), f.spec)
			require.ErrorIs(t, err, js.ErrNoHandler)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, "worker.js")
		})
	}

	f := newFixture(t, `export const api = { fetch() { return new Response("named"); } };`)
	f.spec.Settings = map[string]string{js.SettingCall: "api"}
	require.Equal(t, "named", string(serve(t, f.start(), get("/")).Body), "execution/call names the export")
}

// TestAnEntryThatDoesNotEvaluateIsRefused, naming the file and the reason.
func TestAnEntryThatDoesNotEvaluateIsRefused(t *testing.T) {
	cases := map[string]struct{ worker, want string }{
		"a syntax error":    {`export default {`, "SyntaxError"},
		"a top-level throw": {`throw new Error("refusing to load");`, "refusing to load"},
		"an unknown import": {`import x from "./missing.js"; export default x;`, `"./missing.js"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, c.worker)
			_, err := f.tier.Start(t.Context(), f.spec)
			require.ErrorContains(t, err, "evaluating worker.js")
			require.ErrorContains(t, err, c.want)
		})
	}
}

// TestAModuleThatNeverFinishesLoadingHitsTheDeadline: a cold start is bounded
// as a request is, top-level await included.
func TestAModuleThatNeverFinishesLoadingHitsTheDeadline(t *testing.T) {
	for name, worker := range map[string]string{
		"a top-level loop":  `for (;;) {} export default {};`,
		"a top-level await": `await new Promise(() => {}); export default {};`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, worker)
			f.spec.Timeout = 100 * time.Millisecond
			began := time.Now()
			_, err := f.tier.Start(t.Context(), f.spec)
			require.ErrorIs(t, err, js.ErrDeadline)
			require.Less(t, time.Since(began), time.Second)
		})
	}

	f := newFixture(t, `await later(20); export default { fetch() { return new Response("awaited"); } };`)
	require.Equal(t, "awaited", string(serve(t, f.start(), get("/")).Body), "a top-level await that settles loads")
}

// TestARealmTooSmallToStartIsRefused by name, with the size it was given, and
// a bundle that does not fit its arena is refused as out of memory. Neither
// hands back an instance.
func TestARealmTooSmallToStartIsRefused(t *testing.T) {
	f := newFixture(t, hello)
	f.spec.Memory = 256 << 10
	inst, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrEngine)
	require.ErrorContains(t, err, "262144 bytes")
	require.True(t, inst == nil, "a failed start is a nil Instance, not a nil *instance inside one")

	f = newFixture(t, `const big = "x".repeat(1 << 20) + "y".repeat(1 << 20); export default { fetch() { return new Response(big); } };`)
	f.spec.Memory = 1 << 20
	inst, err = f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrOutOfMemory)
	require.True(t, inst == nil)
}

// TestModulesResolveInsideTheArchive: relative, parent and root-anchored
// imports between the archive's own modules.
func TestModulesResolveInsideTheArchive(t *testing.T) {
	f := newFixtureFiles(t, map[string]string{
		"worker.js": `
import { greet } from "./lib/greet.js";
import { mark } from "/lib/mark.mjs";
export default { fetch() { return new Response(greet() + mark); } };`,
		"lib/greet.js": `import { name } from "../name.js"; export const greet = () => "hi " + name;`,
		"lib/mark.mjs": `export const mark = "!";`,
		"name.js":      `export const name = "there";`,
	})
	require.Equal(t, "hi there!", string(serve(t, f.start(), get("/")).Body))
}

// TestNothingOfTheHostReachesTheRealm: no process, no require, and an import
// of a built-in or a package is a TypeError naming it.
func TestNothingOfTheHostReachesTheRealm(t *testing.T) {
	f := newFixtureFiles(t, map[string]string{
		"worker.js": `
const tryImport = (s) => import(s).then(() => "loaded " + s, (e) => e.constructor.name + ": " + e.message);
export default {
  async fetch() {
    const seen = [typeof process, typeof require];
    seen.push(await tryImport("node:fs"));
    seen.push(await tryImport("lodash"));
    seen.push(await tryImport("./notes.txt"));
    seen.push(await tryImport("../outside.js"));
    seen.push(await import("./data.js", { with: { type: "json" } }).then(() => "loaded", (e) => e.message));
    return new Response(seen.join("\n"));
  },
};`,
		"notes.txt": "not a module",
		"data.js":   `export default 1;`,
	})
	lines := strings.Split(string(serve(t, f.start(), get("/")).Body), "\n")
	require.Equal(t, []string{"undefined", "undefined"}, lines[:2])
	require.Contains(t, lines[2], `TypeError: `)
	require.Contains(t, lines[2], `"node:fs" is a package or a built-in`)
	require.Contains(t, lines[3], `"lodash" is a package or a built-in`)
	require.Contains(t, lines[4], `no such module in the archive: "./notes.txt"`)
	require.Contains(t, lines[5], `no such module in the archive: "../outside.js"`)
	require.Contains(t, lines[6], "imported with attributes")
}

func zipOf(t *testing.T, members map[string][]byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range members {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	return buf.Bytes()
}

func manifest(entry string, format int) []byte {
	return []byte(`{"format":` + string(rune('0'+format)) + `,"entry":"` + entry + `"}`)
}

// TestACorruptArchiveIsRefusedByName, each way an artifact can fail to be
// one, before any realm is started for it.
func TestACorruptArchiveIsRefusedByName(t *testing.T) {
	worker := []byte(hello)
	good := zipOf(t, map[string][]byte{archive.ManifestName: manifest("worker.js", 1), "worker.js": worker}, zip.Deflate)
	flipped := bytes.Clone(good)
	flipped[bytes.Index(flipped, []byte("worker.js"))+len("worker.js")+20] ^= 0xff

	raw := func(method uint16, claimed uint64) []byte {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		w, err := z.CreateRaw(&zip.FileHeader{Name: "worker.js", Method: method, CompressedSize64: 1, UncompressedSize64: claimed})
		require.NoError(t, err)
		_, err = w.Write([]byte("x"))
		require.NoError(t, err)
		require.NoError(t, z.Close())
		return buf.Bytes()
	}

	cases := map[string]struct {
		data []byte
		want error
		says string
	}{
		"not a zip":            {[]byte("this is not a zip"), js.ErrBadArchive, "zip"},
		"no manifest":          {zipOf(t, map[string][]byte{"worker.js": worker}, zip.Deflate), js.ErrBadArchive, "no manifest.json"},
		"a manifest not JSON":  {zipOf(t, map[string][]byte{archive.ManifestName: []byte("{"), "worker.js": worker}, zip.Deflate), js.ErrBadArchive, "its manifest"},
		"a newer format":       {zipOf(t, map[string][]byte{archive.ManifestName: manifest("worker.js", 2), "worker.js": worker}, zip.Deflate), js.ErrArchiveFormat, "format 2"},
		"no entry":             {zipOf(t, map[string][]byte{archive.ManifestName: manifest("main.js", 1), "worker.js": worker}, zip.Deflate), js.ErrNoEntry, `"main.js"`},
		"an entry not JS":      {zipOf(t, map[string][]byte{archive.ManifestName: manifest("worker.txt", 1), "worker.txt": worker}, zip.Deflate), js.ErrNoEntry, `"worker.txt"`},
		"an orphaned compile":  {zipOf(t, map[string][]byte{archive.ManifestName: manifest("worker.js", 1), "worker.js": worker, "gone.js.warp": nil}, zip.Deflate), js.ErrBadArchive, "compiled form"},
		"a climbing member":    {zipOf(t, map[string][]byte{archive.ManifestName: manifest("worker.js", 1), "worker.js": worker, "../x.js": nil}, zip.Deflate), js.ErrBadArchive, "clean relative path"},
		"a damaged member":     {flipped, js.ErrBadArchive, "worker.js"},
		"an unknown method":    {raw(99, 1), js.ErrBadArchive, "algorithm"},
		"a member past budget": {raw(zip.Store, 1<<40), js.ErrBadArchive, "unpacks to more than"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, hello)
			f.store.put("asset", c.data)
			_, err := f.tier.Start(t.Context(), f.spec)
			require.ErrorIs(t, err, c.want)
			require.ErrorContains(t, err, c.says)
		})
	}
}

func TestAnArchiveLargerThanTheCeilingIsRefused(t *testing.T) {
	f := newFixture(t, hello, js.WithMaxArchive(64))
	_, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrBadArchive)
	require.ErrorContains(t, err, "larger than 64 bytes")

	members := map[string][]byte{archive.ManifestName: manifest("worker.js", 1), "worker.js": []byte(hello + strings.Repeat(" ", 4096))}
	f = newFixture(t, hello, js.WithMaxArchive(1024))
	f.store.put("asset", zipOf(t, members, zip.Deflate))
	_, err = f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrBadArchive, "it packs small and unpacks past the ceiling")
	require.ErrorContains(t, err, "unpacks to more than")
}

// TestAnArchiveFromAnotherToolStillReads: directory entries are skipped, and
// a member named twice is refused rather than one of them silently winning.
func TestAnArchiveFromAnotherToolStillReads(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, m := range []struct{ name, body string }{
		{"lib/", ""}, {archive.ManifestName, string(manifest("worker.js", 1))}, {"worker.js", hello},
	} {
		w, err := z.Create(m.name)
		require.NoError(t, err)
		_, err = w.Write([]byte(m.body))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	f := newFixture(t, hello)
	f.store.put("asset", buf.Bytes())
	require.Equal(t, "hello GET", string(serve(t, f.start(), get("/")).Body))

	buf.Reset()
	z = zip.NewWriter(&buf)
	for _, m := range []string{archive.ManifestName, "worker.js", "worker.js"} {
		w, err := z.Create(m)
		require.NoError(t, err)
		body := hello
		if m == archive.ManifestName {
			body = string(manifest("worker.js", 1))
		}
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	f.store.put("asset", buf.Bytes())
	_, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, js.ErrBadArchive)
	require.ErrorContains(t, err, `"worker.js" twice`)
}

// TestACompiledFormShipsBesideItsSource: stage 1 runs the source, and the
// `.warp` slot is accepted as the layout defines it.
func TestACompiledFormShipsBesideItsSource(t *testing.T) {
	prog, err := engine.Compile("worker.js", `1 + 1`)
	require.NoError(t, err)
	compiled, err := prog.Marshal()
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, archive.Write(&buf, "worker.js", map[string][]byte{
		"worker.js": []byte(hello), "worker.js.warp": compiled, "worker.js.map": []byte(`{"version":3}`),
	}))
	f := newFixture(t, hello)
	f.store.put("asset", buf.Bytes())
	require.Equal(t, "hello GET", string(serve(t, f.start(), get("/")).Body))
}

func TestARealmHookThatFailsStopsTheStart(t *testing.T) {
	refused := errors.New("the host refused the realm")
	f := newFixture(t, hello, js.WithRealm(func(*engine.Runtime) error { return refused }))
	_, err := f.tier.Start(t.Context(), f.spec)
	require.ErrorIs(t, err, refused)
}
