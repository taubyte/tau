package docker

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/klauspost/compress/zstd"
	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// member is one entry of a build context a test writes.
type member struct {
	name string
	kind byte
	body string
	link string
}

// regular is a Dockerfile that labels the image it builds with pick, so the
// image says which one was built.
func regular(name, pick string) member {
	return member{name: name, kind: tar.TypeReg, body: "FROM scratch\nLABEL pick=" + pick + "\n"}
}

func directory(name string) member { return member{name: name, kind: tar.TypeDir} }
func symlink(name, target string) member {
	return member{name: name, kind: tar.TypeSymlink, link: target}
}
func hardlink(name, target string) member {
	return member{name: name, kind: tar.TypeLink, link: target}
}

// contextOf is an uncompressed build context of members, in order.
func contextOf(t *testing.T, members ...member) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, m := range members {
		header := &tar.Header{Name: m.name, Typeflag: m.kind, Linkname: m.link, Mode: 0o644, Size: int64(len(m.body))}
		if m.kind == tar.TypeXGlobalHeader {
			header = &tar.Header{Typeflag: m.kind, PAXRecords: map[string]string{"comment": m.name}}
		}
		require.NoError(t, writer.WriteHeader(header))
		_, err := writer.Write([]byte(m.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

// bzip2Context is contextOf(regular("Dockerfile", "A")), made with bzip2(1):
// the standard library reads bzip2 and does not write it.
const bzip2Context = "QlpoOTFBWSZTWcut/3EAADXfkcqQQAB9hjcGkQBrbN4ABAAAgAAIIAB1FTTRoGhppkNA0AzKDJTTQAAAAAA1giviG1gEPcrhQKecaBCAzaKstXGhLKAnBilCDKIxa0smOXrGxItHVu7nIIGxfXhBR3gEZJ38BJAwolGQn8XckU4UJDLrf9xA"

// daemonContext is a build context and what the daemon makes of it. pick is
// the label of the Dockerfile it builds, which the spool must read; refused is
// part of the error the spool refuses it with instead; and with neither, there
// is no Dockerfile to read and the daemon builds nothing.
type daemonContext struct {
	name       string
	context    []byte
	dockerfile string
	pick       string
	refused    string
}

// daemonContexts are the contexts the spool is held to. What the daemon builds
// from each is checked against a real one by the docker_integration test
// beside this.
func daemonContexts(t *testing.T) []daemonContext {
	t.Helper()

	plain := contextOf(t, regular("Dockerfile", "A"))
	var gz bytes.Buffer
	gzipped := gzip.NewWriter(&gz)
	_, err := gzipped.Write(plain)
	require.NoError(t, err)
	require.NoError(t, gzipped.Close())
	zs, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	bz, err := base64.StdEncoding.DecodeString(bzip2Context)
	require.NoError(t, err)

	return []daemonContext{
		{name: "the last of two entries of one name", context: contextOf(t, regular("Dockerfile", "A"), regular("Dockerfile", "B")), pick: "B"},
		{name: "a name spelled with ./", context: contextOf(t, regular("Dockerfile", "A"), regular("./Dockerfile", "B")), pick: "B"},
		{name: "an absolute name", context: contextOf(t, regular("/Dockerfile", "A")), pick: "A"},
		{name: "a gzip context", context: gz.Bytes(), pick: "A"},
		{name: "a bzip2 context", context: bz, pick: "A"},
		{name: "a zstd context", context: zs.EncodeAll(plain, nil), pick: "A"},
		{name: "an xz context", context: append([]byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, plain...), refused: "xz"},
		{name: "a Dockerfile that is a link", context: contextOf(t, regular("real", "A"), symlink("Dockerfile", "real")), pick: "A"},
		{name: "a link with an absolute target", context: contextOf(t, regular("real", "A"), symlink("Dockerfile", "/real")), pick: "A"},
		{name: "a link climbing above the context", context: contextOf(t, regular("real", "A"), symlink("Dockerfile", "../../real")), pick: "A"},
		{name: "a link in a directory reached through a link", context: contextOf(t, regular("x/real", "A"), directory("x/y/"), symlink("l", "x/y"), symlink("Dockerfile", "l/../real")), pick: "A"},
		{name: "a hard link", context: contextOf(t, regular("real", "A"), hardlink("Dockerfile", "real")), pick: "A"},
		{name: "a hard link whose target is replaced after it", context: contextOf(t, regular("real", "A"), hardlink("Dockerfile", "real"), regular("real", "B")), pick: "A"},
		{name: "a hard link to a link", context: contextOf(t, regular("real", "A"), directory("sub/"), symlink("sub/l", "../real"), hardlink("Dockerfile", "sub/l")), pick: "A"},
		{name: "a hard link through a linked directory", context: contextOf(t, regular("x/real", "A"), symlink("l", "x"), hardlink("Dockerfile", "l/real")), pick: "A"},
		{name: "the lowercase default", context: contextOf(t, regular("dockerfile", "A")), pick: "A"},
		{name: "the lowercase default when Dockerfile leads nowhere", context: contextOf(t, symlink("Dockerfile", "missing"), regular("dockerfile", "B")), pick: "B"},
		{name: "Dockerfile before dockerfile", context: contextOf(t, regular("Dockerfile", "A"), regular("dockerfile", "B")), pick: "A"},
		{name: "no lowercase default for another name", context: contextOf(t, regular("sub/dockerfile", "A")), dockerfile: "sub/Dockerfile"},
		{name: "an entry written through a linked directory", context: contextOf(t, symlink("d", "."), regular("d/Dockerfile", "A")), pick: "A"},
		{name: "an entry written through a linked directory over another", context: contextOf(t, regular("Dockerfile", "A"), symlink("d", "."), regular("d/Dockerfile", "B")), pick: "B"},
		{name: "an entry written through a link that led nowhere", context: contextOf(t, symlink("l", "missing"), regular("l/Dockerfile", "A")), dockerfile: "missing/Dockerfile", pick: "A"},
		{name: "a directory replaced by a link", context: contextOf(t, regular("sub/Dockerfile", "A"), regular("other/Dockerfile", "B"), symlink("sub", "other")), dockerfile: "sub/Dockerfile", pick: "B"},
		{name: "a directory made through a link", context: contextOf(t, directory("real/"), symlink("l", "real"), regular("l/x/Dockerfile", "A")), dockerfile: "real/x/Dockerfile", pick: "A"},
		{name: "an entry naming the root", context: contextOf(t, regular("Dockerfile", "A"), member{name: ".", kind: tar.TypeReg}), pick: "A"},
		{name: "a global header", context: contextOf(t, member{name: "global", kind: tar.TypeXGlobalHeader}, regular("Dockerfile", "A")), pick: "A"},
		{name: "a directory entry after what is in it", context: contextOf(t, regular("sub/Dockerfile", "A"), directory("sub/")), dockerfile: "sub/Dockerfile", pick: "A"},
		{name: "a configured path under nothing", context: plain, dockerfile: "missing/Dockerfile"},
		{name: "a configured path", context: contextOf(t, regular("sub/Custom", "A")), dockerfile: "sub/Custom", pick: "A"},
		{name: "a configured path spelled another way", context: contextOf(t, regular("sub/Custom", "A")), dockerfile: "./sub/../sub/Custom", pick: "A"},
		{name: "an absolute configured path", context: contextOf(t, regular("sub/Custom", "A")), dockerfile: "/sub/Custom", pick: "A"},
		{name: "a configured path climbing out", context: contextOf(t, regular("sub/Custom", "A")), dockerfile: "../sub/Custom", refused: "outside the build context"},
		{name: "a Dockerfile that is a directory", context: contextOf(t, directory("Dockerfile/"), regular("dockerfile", "B")), refused: "not a regular file"},
		{name: "a Dockerfile that is a device", context: contextOf(t, regular("Dockerfile", "A"), member{name: "Dockerfile", kind: tar.TypeChar}), refused: "not a regular file"},
		{name: "a Dockerfile under a file", context: contextOf(t, regular("sub/Dockerfile", "A"), member{name: "sub", kind: tar.TypeReg}), dockerfile: "sub/Dockerfile", refused: "not a directory"},
		{name: "an entry climbing out of the context", context: contextOf(t, regular("../Dockerfile", "A")), refused: "outside the build context"},
		{name: "an entry under a file", context: contextOf(t, member{name: "sub", kind: tar.TypeReg}, regular("sub/Dockerfile", "A")), dockerfile: "sub/Dockerfile", refused: "not a directory"},
		{name: "a hard link climbing out of the context", context: contextOf(t, regular("real", "A"), hardlink("Dockerfile", "../real")), refused: "outside the build context"},
		{name: "an entry two levels under a file", context: contextOf(t, member{name: "sub", kind: tar.TypeReg}, regular("sub/x/Dockerfile", "A")), refused: "not a directory"},
		{name: "a hard link under a file", context: contextOf(t, member{name: "sub", kind: tar.TypeReg}, hardlink("Dockerfile", "sub/x/real")), refused: "not a directory"},
		{name: "a gzip context cut short", context: gz.Bytes()[:6], refused: "unexpected EOF"},
		{name: "a context cut short", context: plain[:100], refused: "unexpected EOF"},
		{name: "a hard link to a directory", context: contextOf(t, directory("d/"), hardlink("Dockerfile", "d")), refused: "hard link"},
		{name: "links in a loop", context: contextOf(t, symlink("Dockerfile", "a"), symlink("a", "Dockerfile")), refused: "too many links"},
		{name: "an entry the daemon does not unpack", context: contextOf(t, regular("Dockerfile", "A"), member{name: "c", kind: tar.TypeCont}), refused: "does not unpack"},
	}
}

// roomy is the reserve of a disk with room to spare, so what a test holds the
// spool to is the spooling and not the disk it runs on.
var roomy = reserve.Floor{Space: 0.15, Most: 4 << 30, Inodes: 0.05, Measure: func(string) (reserve.Usage, error) {
	return reserve.Usage{Size: 1 << 40, Free: 1 << 39, Files: 1 << 20, FreeFiles: 1 << 19}, nil
}}

func spool(t *testing.T, context []byte) (*buildContext, error) {
	t.Helper()
	spooled, err := spoolContext(bytes.NewReader(context), t.TempDir(), roomy)
	if err == nil {
		t.Cleanup(spooled.remove)
	}
	return spooled, err
}

// sent is what the daemon reads of one entry of a context.
type sent struct {
	name, link string
	kind       byte
	mode       int64
	pax        map[string]string
	body       string
}

func entriesOf(t *testing.T, context io.Reader) []sent {
	t.Helper()
	stream, done, err := decompressed(context)
	require.NoError(t, err)
	defer done()

	var entries []sent
	reader := tar.NewReader(stream)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		require.NoError(t, err)
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		entries = append(entries, sent{name: header.Name, link: header.Linkname, kind: header.Typeflag, mode: header.Mode, pax: header.PAXRecords, body: string(body)})
	}
}

// spooledSize is how much of a spool under dir is on disk.
func spooledSize(dir string) (int64, error) {
	files, err := os.ReadDir(dir)
	if err != nil || len(files) == 0 {
		return 0, err
	}
	info, err := files[0].Info()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// TestSpoolReadsWhatTheDaemonBuilds: the Dockerfile read here is checked
// before a restricted build, so it has to be the one the daemon then builds,
// out of a context that holds what the one sent held, or the context is
// refused.
func TestSpoolReadsWhatTheDaemonBuilds(t *testing.T) {
	for _, c := range daemonContexts(t) {
		t.Run(c.name, func(t *testing.T) {
			spooled, err := spool(t, c.context)
			var (
				header *tar.Header
				body   []byte
			)
			if err == nil {
				header, body, _, err = spooled.dockerfile(cmp.Or(c.dockerfile, "Dockerfile"))
			}
			if c.refused != "" {
				require.ErrorContains(t, err, c.refused)
				return
			}
			require.NoError(t, err)

			context, err := spooled.finish()
			require.NoError(t, err)
			assert.Equal(t, entriesOf(t, bytes.NewReader(c.context)), entriesOf(t, context), "the daemon is sent every entry as it was read")
			if c.pick == "" {
				assert.Nil(t, header)
				return
			}
			assert.Equal(t, regular("", c.pick).body, string(body))
		})
	}
}

// TestADockerfilePutWhereItLiesIsTheOneRead: the pinned Dockerfile goes where
// the one read lies once its links are followed, so it is the one opened.
func TestADockerfilePutWhereItLiesIsTheOneRead(t *testing.T) {
	pinned := regular("", "pinned").body
	for _, c := range daemonContexts(t) {
		if c.refused != "" || c.pick == "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			name := cmp.Or(c.dockerfile, "Dockerfile")
			spooled, err := spool(t, c.context)
			require.NoError(t, err)
			_, _, at, err := spooled.dockerfile(name)
			require.NoError(t, err)

			require.NoError(t, spooled.put(&tar.Header{Typeflag: tar.TypeReg, Name: at, Mode: 0o644, Size: int64(len(pinned))}, strings.NewReader(pinned), nil))
			_, body, again, err := spooled.dockerfile(name)
			require.NoError(t, err)
			assert.Equal(t, at, again)
			assert.Equal(t, pinned, string(body))
		})
	}
}

// TestSpoolWritesTheContextOutAsItReadsIt: a context is as big as the
// repository it is built from, so it goes to disk as it arrives, with no more
// than a chunk or two of it in memory at any time.
func TestSpoolWritesTheContextOutAsItReadsIt(t *testing.T) {
	const pieces = 16
	dir := t.TempDir()
	reader, writer := io.Pipe()
	go func() {
		writer.CloseWithError(func() error {
			archive := tar.NewWriter(writer)
			if err := archive.WriteHeader(&tar.Header{Name: "big", Mode: 0o644, Size: pieces * reserve.Chunk}); err != nil {
				return err
			}
			piece := bytes.Repeat([]byte{1}, reserve.Chunk)
			for k := range pieces {
				// Everything sent before the last three chunks has to be on
				// disk: one is in the copy, one in the write buffer.
				on, err := spooledSize(dir)
				if err != nil {
					return err
				}
				if k >= 3 && on < int64(k-3)*reserve.Chunk {
					return fmt.Errorf("%d chunks of the context sent and %d bytes of it on disk", k, on)
				}
				if _, err := archive.Write(piece); err != nil {
					return err
				}
			}
			return archive.Close()
		}())
	}()

	spooled, err := spoolContext(reader, dir, roomy)
	require.NoError(t, err)
	defer spooled.remove()
	context, err := spooled.finish()
	require.NoError(t, err)
	entries := entriesOf(t, context)
	require.Len(t, entries, 1)
	assert.Len(t, entries[0].body, pieces*reserve.Chunk)
}

// TestSpoolLeavesTheDiskItsReserve: the spool is read against the reserve
// before every chunk, and one that would pass it is refused and removed.
func TestSpoolLeavesTheDiskItsReserve(t *testing.T) {
	dir := t.TempDir()
	// A 16MiB disk with 12MiB free, half of it held back: 4MiB of spool fit.
	floor := reserve.Floor{Space: 0.5, Measure: func(string) (reserve.Usage, error) {
		on, err := spooledSize(dir)
		return reserve.Usage{Size: 16 << 20, Free: 12<<20 - uint64(on)}, err
	}}

	var refused *reserve.Error
	big := contextOf(t, member{name: "big", kind: tar.TypeReg, body: strings.Repeat("x", 6<<20)})
	_, err := spoolContext(bytes.NewReader(big), dir, floor)
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "bytes", refused.Unit)
	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, left, "a refused context leaves nothing behind")

	small := contextOf(t, member{name: "small", kind: tar.TypeReg, body: strings.Repeat("x", 1<<20)})
	spooled, err := spoolContext(bytes.NewReader(small), dir, floor)
	require.NoError(t, err)
	spooled.remove()

	full := reserve.Floor{Inodes: 0.05, Measure: func(string) (reserve.Usage, error) {
		return reserve.Usage{Files: 100, FreeFiles: 5}, nil
	}}
	_, err = spoolContext(bytes.NewReader(small), dir, full)
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "inodes", refused.Unit)
}

func TestSpoolSaysAContextItCouldNotRead(t *testing.T) {
	dir := t.TempDir()
	_, err := spoolContext(iotest.ErrReader(errors.New("the upload stopped")), dir, roomy)
	require.ErrorContains(t, err, "the upload stopped")
	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, left)

	_, err = spoolContext(bytes.NewReader(nil), dir+"/missing", roomy)
	require.ErrorContains(t, err, "spooling build context")
}

func TestSpoolRefusesADockerfileTooBigToCheck(t *testing.T) {
	spooled, err := spool(t, contextOf(t, member{name: "Dockerfile", kind: tar.TypeReg, body: strings.Repeat("#", maxDockerfile+1)}))
	require.NoError(t, err)
	_, _, _, err = spooled.dockerfile("Dockerfile")
	require.ErrorContains(t, err, "more than the")
}

func TestCheckDockerfile(t *testing.T) {
	const (
		fetches   = "fetches an ADD source"
		onbuild   = "ONBUILD runs it"
		heredoc   = "heredoc"
		directive = "whether it is a parser directive"
		parse     = "parsing the Dockerfile"
	)
	for _, test := range []struct {
		name, dockerfile, refused string
	}{
		{"a local ADD", "FROM alpine\nADD ./src /app\n", ""},
		{"COPY is never remote", "FROM alpine\nCOPY ./src /app\n", ""},
		{"a COPY of a variable", "FROM alpine\nCOPY $SRC /app\n", ""},
		{"a URL in a RUN runs on the build's network", "FROM alpine\nRUN wget http://example.com/x\n", ""},
		{"commented out", "FROM alpine\n# ADD http://example.com/x /x\nRUN true\n", ""},
		{"a comment inside a continuation", "FROM alpine\nRUN echo \\\n# ADD http://example.com/x /x\n  done\n", ""},
		{"a local ADD with a flag", "FROM alpine\nADD --chown=1:1 src /dst\n", ""},
		{"a local ADD in JSON", "FROM alpine\nADD [\"src\", \"/dst\"]\n", ""},
		{"a local ONBUILD ADD", "FROM alpine\nONBUILD ADD . /app\n", ""},
		{"an ONBUILD RUN runs on the build's network", "FROM alpine\nONBUILD RUN wget http://example.com/x\n", ""},
		{"a backtick escape", "# escape=`\nFROM alpine\nRUN echo `\n  done\n", ""},
		{"a syntax directive", "# syntax=docker/dockerfile:1\nFROM alpine\n", ""},
		{"a comment that is no directive", "# builds the app: make=all\nFROM alpine\n", ""},

		{"a remote ADD", "FROM alpine\nADD http://example.com/x /x\n", fetches},
		{"a remote ADD, https", "FROM alpine\nADD https://example.com/x /x\n", fetches},
		{"lowercase", "FROM alpine\nadd http://example.com/x /x\n", fetches},
		{"after a flag", "FROM alpine\nADD --chown=1:1 http://example.com/x /x\n", fetches},
		{"a line continuation", "FROM alpine\nADD \\\n  http://example.com/x \\\n  /x\n", fetches},
		{"a git source", "FROM alpine\nADD git://example.com/r.git /r\n", fetches},
		{"a variable", "FROM alpine\nARG URL=http://example.com/x\nADD $URL /x\n", fetches},
		{"a braced variable", "FROM alpine\nADD ${URL} /x\n", fetches},
		{"a quoted URL", "FROM alpine\nADD \"http://example.com/x\" /x\n", fetches},
		{"a URL split by quotes", "FROM alpine\nADD ht\"tp:\"//example.com/x /x\n", fetches},
		{"a URL split by escapes", "FROM alpine\nADD http:\\/\\/example.com/x /x\n", fetches},
		{"a URL in JSON", "FROM alpine\nADD [\"http://example.com/x\", \"/x\"]\n", fetches},
		{"a URL behind a JSON escape", "FROM alpine\nADD [\"http\\u003a//example.com/x\", \"/x\"]\n", fetches},
		{"a variable behind a JSON escape", "FROM alpine\nADD [\"\\u0024URL\", \"/x\"]\n", fetches},
		{"JSON with a URL after it", "FROM alpine\nADD [\"src\"] http://example.com/x /x\n", fetches},
		{"a line a backtick escape leaves alone", "# escape=`\nFROM alpine\nRUN echo \\\nADD http://example.com/x /x\n", fetches},
		{"a flag a backtick escape does not escape", "# escape=`\nFROM alpine\nADD --chown=1:1\\ http://example.com/x /x\n", fetches},
		{"an ONBUILD ADD", "FROM alpine\nONBUILD ADD http://example.com/x /x\n", onbuild},
		{"a lowercase onbuild add", "FROM alpine\nonbuild add $URL /x\n", onbuild},

		{"a heredoc", "FROM alpine\nRUN <<EOF\necho\nEOF\n", heredoc},
		{"a heredoc only newer parsers read", "FROM alpine\nRUN << EOF\nADD http://example.com/x /x\nEOF\n", heredoc},
		{"a heredoc hiding what a newer parser runs", "FROM alpine\nRUN << A\nRUN <<CMD\nA\nADD http://example.com/x /x\nCMD\n", heredoc},

		{"a directive this parser does not know, before the escape", "# check=skip=all\n# escape=`\nFROM alpine\nRUN echo \\\nADD http://example.com/x /x\n", directive},
		{"a directive after a vertical tab", "#\v escape=`\nFROM alpine\n", directive},
		{"a directive after a no-break space", "#\u00a0escape=`\nFROM alpine\n", directive},
		{"an unknown directive", "# maintainer=me\nFROM alpine\n", directive},
		{"two escape directives", "# escape=`\n# escape=\\\nFROM alpine\n", parse},
		{"an escape the parser refuses", "# escape=x\nFROM alpine\n", parse},
		{"an empty Dockerfile", "", parse},
		{"a line too long to read", "FROM alpine\nRUN " + strings.Repeat("x", 70000) + "\n", parse},
		{"an instruction the check does not know", "FROM alpine\nFETCH http://example.com/x\n", "does not know the instruction"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := checkDockerfile([]byte(test.dockerfile))
			if test.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.refused)
		})
	}
}

func TestCheckTrigger(t *testing.T) {
	require.NoError(t, checkTrigger("RUN echo built"))
	require.NoError(t, checkTrigger("COPY . /app"))
	require.ErrorContains(t, checkTrigger("ADD http://example.com/x /x"), "fetches an ADD source")
	require.ErrorContains(t, checkTrigger("ONBUILD ADD $URL /x"), "ONBUILD runs it")
	require.ErrorContains(t, checkTrigger("RUN true\nADD http://example.com/x /x"), "spans lines")
	require.ErrorContains(t, checkTrigger("# escape=`"), "parsing the Dockerfile")
}

// imageJSON answers an image inspect with an id and build triggers.
func imageJSON(t *testing.T, id string, triggers ...string) http.HandlerFunc {
	t.Helper()
	body, err := json.Marshal(map[string]any{"Id": id, "Config": map[string]any{"OnBuild": triggers}})
	require.NoError(t, err)
	return jsonBody(string(body))
}

// TestPinNamesEachBaseByTheImageChecked: every FROM is written out again with
// nothing left to expand, an image by the id that was checked, on the lines
// the original took.
func TestPinNamesEachBaseByTheImageChecked(t *testing.T) {
	backend, daemon := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/alpine/json":    imageJSON(t, "sha256:aaa"),
		"GET /images/busybox:1/json": imageJSON(t, "sha256:bbb", "RUN echo built"),
	})
	dockerfile := strings.Join([]string{
		"# builds twice",
		"ARG BASE=alp",
		"ARG REST=ine UNSET",
		"FROM ${BASE}${REST} AS build",
		"ARG BASE=busybox",
		"RUN make",
		"FROM build AS test",
		"FROM \\",
		"  busybox:1",
		"COPY --from=build /out /out",
		"FROM --platform=linux/amd64 scratch AS out",
		"FROM Test",
	}, "\n")

	pinned, err := backend.pin(t.Context(), []byte(dockerfile))
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		"# builds twice",
		"ARG BASE=alp",
		"ARG REST=ine UNSET",
		"FROM sha256:aaa AS build",
		"ARG BASE=busybox",
		"RUN make",
		"FROM build AS test",
		"FROM sha256:bbb",
		"",
		"COPY --from=build /out /out",
		"FROM --platform=linux/amd64 scratch AS out",
		"FROM test",
	}, "\n"), string(pinned))
	assert.False(t, daemon.called("POST /images/create"), "a base the daemon has is not pulled")
}

// TestPinPullsABaseTheDaemonDoesNotHave, as the classic builder does when it
// is not told to pull: a base it has is used, one it does not is pulled.
func TestPinPullsABaseTheDaemonDoesNotHave(t *testing.T) {
	inspects := 0
	backend, daemon := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/alpine/json": func(w http.ResponseWriter, r *http.Request) {
			inspects++
			if inspects == 1 {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"no such image"}`)
				return
			}
			imageJSON(t, "sha256:aaa")(w, r)
		},
		"POST /images/create": jsonBody(`{"status":"Downloaded newer image for alpine:latest"}`),
	})

	pinned, err := backend.pin(t.Context(), []byte("FROM alpine\n"))
	require.NoError(t, err)
	assert.Equal(t, "FROM sha256:aaa\n", string(pinned))
	assert.True(t, daemon.called("POST /images/create"))
}

func TestPinRefusesABaseItCannotCheck(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/fetching/json":  imageJSON(t, "sha256:aaa", "ADD http://example.com/x /x"),
		"GET /images/declaring/json": imageJSON(t, "sha256:bbb", "ONBUILD ADD $URL /x"),
		"GET /images/crafted/json":   imageJSON(t, "sha256:ccc", "RUN true\nADD http://example.com/x /x"),
		"GET /images/anonymous/json": imageJSON(t, ""),
	})
	for base, refused := range map[string]string{
		"fetching":  "base image fetching: restricted egress cannot cover",
		"declaring": "base image declaring: ONBUILD runs it",
		"crafted":   "spans lines",
		"anonymous": "has no id",
		"missing":   "base image: failed to pull image missing",
	} {
		t.Run(base, func(t *testing.T) {
			_, err := backend.pin(t.Context(), []byte("FROM "+base+"\n"))
			require.ErrorContains(t, err, refused)
		})
	}

	pulled, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"POST /images/create": jsonBody(`{"status":"Downloaded"}`),
	})
	_, err := pulled.pin(t.Context(), []byte("FROM gone\n"))
	require.ErrorContains(t, err, "base image gone")
}

func TestPinRefusesAFromTheDaemonWouldNotBuild(t *testing.T) {
	backend, daemon := newFakeDaemonBackend(t, nil)
	for _, test := range []struct{ dockerfile, refused string }{
		{"FROM alpine extra\n", "is not `FROM image [AS name]`"},
		{"FROM alpine AS 1bad\n", "names its stage"},
		{"FROM scratch AS a\nFROM scratch AS A\n", "names its stage"},
		{"FROM --foo=bar alpine\n", "takes one flag"},
		{"FROM --platform=linux/amd64 --platform=linux/arm64 alpine\n", "takes one flag"},
		{"FROM $NOTHING\n", "names no base image"},
		{"FROM ${BASE\n", "names no base image"},
		{"FROM --platform=${P scratch\n", "expanding"},
		{"FROM --platform=linux/nonsense/v9/x scratch\n", "--platform"},
		{"ARG BASE=${\nFROM scratch\n", "expanding"},
	} {
		t.Run(test.dockerfile, func(t *testing.T) {
			_, err := backend.pin(t.Context(), []byte(test.dockerfile))
			require.ErrorContains(t, err, test.refused)
		})
	}
	assert.False(t, daemon.called("GET /images/alpine/json"), "a FROM refused as written is not looked up")
}

// TestPinInspectsTheBaseForItsPlatform: the triggers checked are the ones of
// the image the daemon builds FROM, which for --platform is that platform's.
func TestPinInspectsTheBaseForItsPlatform(t *testing.T) {
	daemon := &fakeDaemon{routes: map[string]http.HandlerFunc{
		"GET /images/alpine/json": func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Query().Get("platform"), "arm64") {
				imageJSON(t, "sha256:amd", "ADD http://example.com/x /x")(w, r)
				return
			}
			imageJSON(t, "sha256:arm")(w, r)
		},
	}}
	server := httptest.NewServer(daemon)
	t.Cleanup(server.Close)
	backend, err := New(core.DockerConfig{Host: "tcp://" + strings.TrimPrefix(server.URL, "http://"), APIVersion: "1.49"})
	require.NoError(t, err)

	pinned, err := backend.pin(t.Context(), []byte("FROM --platform=linux/arm64 alpine AS a\n"))
	require.NoError(t, err)
	assert.Equal(t, "FROM --platform=linux/arm64 sha256:arm AS a\n", string(pinned))

	_, err = backend.pin(t.Context(), []byte("FROM alpine\n"))
	require.ErrorContains(t, err, "base image alpine")
}

// TestRestrictContextAppendsThePinnedDockerfile where the daemon opens it,
// after everything the context held.
func TestRestrictContextAppendsThePinnedDockerfile(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/alpine/json": imageJSON(t, "sha256:aaa"),
	})
	original := contextOf(t,
		member{name: "real", kind: tar.TypeReg, body: "FROM alpine\nLABEL x=y\n"},
		symlink("Dockerfile", "real"),
	)

	spooled, err := backend.restrictContext(t.Context(), bytes.NewReader(original), "Dockerfile", t.TempDir(), roomy)
	require.NoError(t, err)
	defer spooled.remove()
	context, err := spooled.finish()
	require.NoError(t, err)

	entries := entriesOf(t, context)
	require.Len(t, entries, 3)
	assert.Equal(t, entriesOf(t, bytes.NewReader(original)), entries[:2])
	assert.Equal(t, sent{name: "real", kind: tar.TypeReg, mode: 0o644, body: "FROM sha256:aaa\nLABEL x=y\n"}, entries[2])
}

func TestRestrictContextRefusesAndLeavesNothing(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, nil)
	dir := t.TempDir()

	_, err := backend.restrictContext(t.Context(), bytes.NewReader(contextOf(t, member{name: "Dockerfile", kind: tar.TypeReg, body: "FROM scratch\nADD http://example.com/x /x\n"})), "Dockerfile", dir, roomy)
	require.ErrorContains(t, err, "fetches an ADD source")
	_, err = backend.restrictContext(t.Context(), iotest.ErrReader(errors.New("the upload stopped")), "Dockerfile", dir, roomy)
	require.ErrorContains(t, err, "the upload stopped")
	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, left)

	// No Dockerfile is the daemon's error to give, so the context goes as it is.
	other := contextOf(t, member{name: "other", kind: tar.TypeReg, body: "x"})
	spooled, err := backend.restrictContext(t.Context(), bytes.NewReader(other), "Dockerfile", dir, roomy)
	require.NoError(t, err)
	defer spooled.remove()
	context, err := spooled.finish()
	require.NoError(t, err)
	assert.Equal(t, entriesOf(t, bytes.NewReader(other)), entriesOf(t, context))
}

// fetchCase is a Dockerfile and whether the check refuses it. Built by a real
// daemon, a refused one fetches {{URL}}, unless quiet says the check refuses
// more there than the daemon fetches; one let through never fetches. The
// docker_integration test beside this builds each to hold the check to that.
type fetchCase struct {
	name, dockerfile string
	refused, quiet   bool
}

// fetchContext is what each fetchCase is built with: files for its local
// sources, and the names a heredoc's own line reads as sources to a parser
// that does not see the heredoc.
func fetchContext(t *testing.T, dockerfile string) []byte {
	t.Helper()
	return contextOf(t,
		member{name: "Dockerfile", kind: tar.TypeReg, body: dockerfile},
		member{name: "src", kind: tar.TypeReg, body: "src"},
		member{name: `["src"]`, kind: tar.TypeReg, body: "src"},
		member{name: "<<", kind: tar.TypeReg, body: "<<"},
		member{name: "A", kind: tar.TypeReg, body: "A"},
	)
}

func fetchCases(host, at string) []fetchCase {
	url := "http://" + host + at
	cases := []fetchCase{
		{name: "a local ADD", dockerfile: "FROM scratch\nADD src /src\n"},
		{name: "a local ADD in JSON", dockerfile: "FROM scratch\nADD [\"src\", \"/src\"]\n"},
		{name: "a commented-out ADD", dockerfile: "FROM scratch\n# ADD {{URL}} /x\nLABEL a=b\n"},
		{name: "a comment inside a continuation", dockerfile: "FROM scratch\nLABEL a=b \\\n# ADD {{URL}} /x\n  c=d\n"},
		{name: "a backtick escape", dockerfile: "# escape=`\nFROM scratch\nLABEL a=b `\n  c=d\n"},

		{name: "a remote ADD", dockerfile: "FROM scratch\nADD {{URL}} /x\n", refused: true},
		{name: "after a flag", dockerfile: "FROM scratch\nADD --chown=0:0 {{URL}} /x\n", refused: true},
		{name: "a quoted URL", dockerfile: "FROM scratch\nADD \"{{URL}}\" /x\n", refused: true},
		{name: "a URL split by quotes", dockerfile: "FROM scratch\nADD ht\"tp:\"//{{HOST}}{{AT}} /x\n", refused: true},
		{name: "a URL split by escapes", dockerfile: "FROM scratch\nADD http:\\/\\/{{HOST}}{{AT}} /x\n", refused: true},
		{name: "a URL in JSON", dockerfile: "FROM scratch\nADD [\"{{URL}}\", \"/x\"]\n", refused: true},
		{name: "a URL behind a JSON escape", dockerfile: "FROM scratch\nADD [\"http\\u003a//{{HOST}}{{AT}}\", \"/x\"]\n", refused: true},
		{name: "an ARG", dockerfile: "FROM scratch\nARG URL={{URL}}\nADD $URL /x\n", refused: true},
		{name: "an ENV", dockerfile: "FROM scratch\nENV URL={{URL}}\nADD ${URL} /x\n", refused: true},
		{name: "JSON with a URL after it", dockerfile: "FROM scratch\nADD [\"src\"] {{URL}} /x\n", refused: true},
		{name: "a line a backtick escape leaves alone", dockerfile: "# escape=`\nFROM scratch\nLABEL a \\\nADD {{URL}} /x\n", refused: true},
		{name: "a flag a backtick escape does not escape", dockerfile: "# escape=`\nFROM scratch\nADD --chown=0:0\\ {{URL}} /x\n", refused: true},
		{name: "an ONBUILD ADD a later stage runs", dockerfile: "FROM scratch AS base\nONBUILD ADD {{URL}} /x\nFROM base\n", refused: true},
		{name: "a heredoc only a newer parser reads", dockerfile: "FROM scratch\nCOPY << A /x/\nCOPY <<CMD /y/\nA\nADD {{URL}} /z\nCMD\n", refused: true},
		{name: "a directive this parser does not know, before the escape", dockerfile: "# check=skip=all\n# escape=`\nFROM scratch\nLABEL a \\\nADD {{URL}} /x\n", refused: true},
		{name: "a directive after a vertical tab", dockerfile: "#\v escape=`\nFROM scratch\nLABEL a \\\nADD {{URL}} /x\n", refused: true},
		{name: "a directive after a no-break space", dockerfile: "#\u00a0escape=`\nFROM scratch\nLABEL a \\\nADD {{URL}} /x\n", refused: true},

		{name: "an ONBUILD ADD nothing runs", dockerfile: "FROM scratch\nONBUILD ADD {{URL}} /x\n", refused: true, quiet: true},
		{name: "a variable in the destination", dockerfile: "FROM scratch\nADD src $DIR/\n", refused: true, quiet: true},
	}
	fill := strings.NewReplacer("{{URL}}", url, "{{HOST}}", host, "{{AT}}", at)
	for i := range cases {
		cases[i].dockerfile = fill.Replace(cases[i].dockerfile)
	}
	return cases
}

func TestFetchCasesAreCheckedAsTheySay(t *testing.T) {
	for _, c := range fetchCases("example.com", "/x") {
		t.Run(c.name, func(t *testing.T) {
			_, err := checkDockerfile([]byte(c.dockerfile))
			assert.Equal(t, c.refused, err != nil, "%v", err)
		})
	}
}

// TestARestrictedBuildIsClassicOnTheRestrictedNetwork: what the check holds a
// Dockerfile to is what the classic builder fetches, so that is the builder a
// restricted build asks for, whatever the daemon's default.
func TestARestrictedBuildIsClassicOnTheRestrictedNetwork(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, nil)
	var options client.ImageBuildOptions
	spooled, err := backend.restrictBuild(t.Context(), &core.DockerfileBuild{
		Context:        bytes.NewReader(contextOf(t, member{name: "Dockerfile", kind: tar.TypeReg, body: "FROM scratch\n"})),
		RestrictEgress: true,
	}, &options, t.TempDir(), roomy)
	require.NoError(t, err)
	spooled.remove()
	assert.Equal(t, build.BuilderV1, options.Version)
	assert.Equal(t, restrictedNetwork, options.NetworkMode)
}
