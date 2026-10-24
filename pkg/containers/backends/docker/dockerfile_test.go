package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"testing"
	"testing/iotest"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteADD(t *testing.T) {
	for _, test := range []struct {
		name       string
		dockerfile string
		found      bool
	}{
		{"local ADD", "FROM alpine\nADD ./src /app\n", false},
		{"COPY is never remote", "FROM alpine\nCOPY ./src /app\n", false},
		{"remote ADD", "FROM alpine\nADD http://example.com/x /x\n", true},
		{"remote ADD, https", "FROM alpine\nADD https://example.com/x /x\n", true},
		{"lowercase", "FROM alpine\nadd http://example.com/x /x\n", true},
		{"after a flag", "FROM alpine\nADD --chown=1:1 http://example.com/x /x\n", true},
		{"line continuation", "FROM alpine\nADD \\\n  http://example.com/x \\\n  /x\n", true},
		{"commented out", "FROM alpine\n# ADD http://example.com/x /x\nRUN true\n", false},
		{"git source", "FROM alpine\nADD git://example.com/r.git /r\n", true},
		{"url in a RUN is not our business", "FROM alpine\nRUN wget http://example.com/x\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, found := remoteADD([]byte(test.dockerfile))
			assert.Equal(t, test.found, found, "source=%q", source)
		})
	}
}

func TestReadDockerfileKeepsTheContextUsable(t *testing.T) {
	const body = "FROM alpine\nADD http://example.com/x /x\n"

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(body))}))
	_, err := writer.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	dockerfile, buffered, err := readDockerfile(&buf, "Dockerfile")
	require.NoError(t, err)
	assert.Equal(t, body, string(dockerfile))

	// The daemon still has to receive the whole context after we have read it.
	reader := tar.NewReader(bytes.NewReader(buffered))
	header, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "Dockerfile", header.Name)
}

func TestReadDockerfileSaysAContextItCouldNotRead(t *testing.T) {
	_, _, err := readDockerfile(iotest.ErrReader(errors.New("the upload stopped")), "Dockerfile")
	require.ErrorContains(t, err, "the upload stopped")
}

func TestReadDockerfileMissing(t *testing.T) {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "other", Mode: 0o644, Size: 0}))
	require.NoError(t, writer.Close())

	dockerfile, buffered, err := readDockerfile(&buf, "Dockerfile")
	require.NoError(t, err, "a context without one is the daemon's error to give, not ours")
	assert.Nil(t, dockerfile)
	assert.NotEmpty(t, buffered)
}

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
// the label of the Dockerfile it builds, which readDockerfile must return;
// refused is part of the error readDockerfile refuses it with instead; and
// with neither, there is no Dockerfile to read and the daemon builds nothing.
type daemonContext struct {
	name       string
	context    []byte
	dockerfile string
	pick       string
	refused    string
}

// daemonContexts are the contexts readDockerfile is held to. What the daemon
// builds from each is checked against a real one by the docker_integration
// test beside this.
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

// TestReadDockerfileReadsWhatTheDaemonBuilds: the Dockerfile read here is
// checked before a restricted build, so it has to be the one the daemon then
// builds, or the context is refused.
func TestReadDockerfileReadsWhatTheDaemonBuilds(t *testing.T) {
	for _, c := range daemonContexts(t) {
		t.Run(c.name, func(t *testing.T) {
			name := c.dockerfile
			if name == "" {
				name = "Dockerfile"
			}
			dockerfile, buffered, err := readDockerfile(bytes.NewReader(c.context), name)
			if c.refused != "" {
				require.ErrorContains(t, err, c.refused)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.context, buffered, "the daemon has to receive the context as it came")
			if c.pick == "" {
				assert.Nil(t, dockerfile)
				return
			}
			assert.Equal(t, regular("", c.pick).body, string(dockerfile))
		})
	}
}
