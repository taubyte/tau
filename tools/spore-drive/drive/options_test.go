package drive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/afero"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/mycelium/host/mocks"
	"github.com/taubyte/tau/tools/spore-drive/config/fixtures"
	"github.com/taubyte/tau/tools/spore-drive/course"
	"gopkg.in/ini.v1"
	"gotest.tools/v3/assert"
)

func tarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	assert.NilError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}))
	_, err := tw.Write(content)
	assert.NilError(t, err)
	assert.NilError(t, tw.Close())
	assert.NilError(t, gz.Close())
	return buf.Bytes()
}

// fromReleases has a spore-drive download from r.
func fromReleases(r releases) Option {
	return func(s Spore) error {
		s.(*sporedrive).releases = r
		return nil
	}
}

// serveReleases publishes a release of tau, as GitHub does, and is where it is.
func serveReleases(t *testing.T) releases {
	t.Helper()
	return serveRelease(t, []byte("tau binary"))
}

// serveRelease publishes v1.2.3 of tau with binary as its taud.
func serveRelease(t *testing.T, binary []byte) releases {
	t.Helper()
	asset := tarball(t, "taud", binary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			io.WriteString(w, `{"tag_name":"v1.2.3"}`)
		case "/download/v1.2.3/tau_1.2.3_linux_amd64.tar.gz":
			w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return releases{latest: srv.URL + "/latest", download: srv.URL + "/download"}
}

func TestTauComesFromWhereAnOptionSays(t *testing.T) {
	r := serveReleases(t)
	_, p := fixtures.VirtConfig()

	for _, opt := range []Option{WithTauLatest(), WithTauVersion("v1.2.3"), WithTauUrl(r.download + "/v1.2.3/tau_1.2.3_linux_amd64.tar.gz")} {
		s, err := New(p, fromReleases(r), opt)
		assert.NilError(t, err)
		assert.Equal(t, string(s.(*sporedrive).tauBinary), "tau binary")
		assert.Assert(t, s.(*sporedrive).tauBinaryHash != "")
	}

	path := filepath.Join(t.TempDir(), "tau")
	assert.NilError(t, os.WriteFile(path, []byte("local"), 0o600))
	s, err := New(p, WithTauPath(path))
	assert.NilError(t, err)
	assert.Equal(t, string(s.(*sporedrive).tauBinary), "local")

	for _, opt := range []Option{WithTauVersion("9.9.9"), WithTauUrl(r.download + "/nothing"), WithTauPath(filepath.Join(t.TempDir(), "missing"))} {
		_, err := New(p, fromReleases(r), opt)
		assert.Assert(t, err != nil)
	}

	undecodable := releases{latest: r.download + "/nothing-to-decode", download: r.download}
	_, err = New(p, fromReleases(undecodable), WithTauLatest())
	assert.ErrorContains(t, err, "fetching latest release")
}

func TestProgressNamesItsHopAndHost(t *testing.T) {
	h := new(mocks.Host)
	h.On("Name").Return("host1")
	p := &progress{hypha: &course.Hypha{Name: "hop"}, host: h, stepName: "step", progress: 50}
	assert.Equal(t, p.Hop(), "hop")
	assert.Equal(t, p.Host(), "host1")
	assert.Equal(t, p.String(), "[hop][host1] step 50")
	p.err = errors.New("broke")
	assert.Equal(t, p.String(), "[hop][host1] step broke")
}

// fsHost is a remote host whose disk is an afero tree and whose sudo copies.
type fsHost struct {
	fs    afero.Fs
	sudo  func(name string, args ...string) error
	input func(stdin []byte, name string, args ...string) error
}

func (f *fsHost) Sudo(_ context.Context, name string, args ...string) ([]byte, error) {
	return nil, f.sudo(name, args...)
}
func (f *fsHost) SudoInput(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	return nil, f.input(stdin, name, args...)
}
func (f *fsHost) Execute(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (f *fsHost) Open(p string) (io.ReadCloser, error)                       { return f.fs.Open(p) }
func (f *fsHost) OpenFile(p string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	return f.fs.OpenFile(p, flag, perm)
}
func (f *fsHost) Remove(p string) error    { return f.fs.Remove(p) }
func (f *fsHost) RemoveAll(p string) error { return f.fs.RemoveAll(p) }
func (f *fsHost) Host() host.Host          { return nil }

func TestResolvedConfIsRewrittenToTheWorkingResolvers(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/etc/systemd/resolved.conf", []byte("[Resolve]\n#DNS=\n"), 0o644))
	h := &fsHost{fs: fs, sudo: func(name string, args ...string) error {
		data, err := afero.ReadFile(fs, args[0])
		if err != nil {
			return err
		}
		return afero.WriteFile(fs, args[1], data, 0o644)
	}}
	ctx := context.Background()

	assert.ErrorContains(t, updateResolvedConf(ctx, h, nil), "no DNS resolvers")
	assert.NilError(t, updateResolvedConf(ctx, h, []string{"1.1.1.1", "8.8.8.8"}))
	got, err := afero.ReadFile(fs, "/etc/systemd/resolved.conf")
	assert.NilError(t, err)
	cfg, err := ini.Load(got)
	assert.NilError(t, err)
	assert.Equal(t, cfg.Section("Resolve").Key("DNS").String(), "1.1.1.1 8.8.8.8")
	assert.Equal(t, cfg.Section("Resolve").Key("DNSStubListener").String(), "no")
	_, err = fs.Stat("/tmp/resolved.conf")
	assert.Assert(t, os.IsNotExist(err), "the temporary file is removed")

	h.sudo = func(string, ...string) error { return errors.New("denied") }
	assert.ErrorContains(t, updateResolvedConf(ctx, h, []string{"1.1.1.1"}), "failed to copy")

	assert.NilError(t, fs.Remove("/etc/systemd/resolved.conf"))
	assert.ErrorContains(t, updateResolvedConf(ctx, h, []string{"1.1.1.1"}), "failed to open")

	assert.NilError(t, afero.WriteFile(fs, "/etc/systemd/resolved.conf", []byte("[broken"), 0o644))
	assert.ErrorContains(t, updateResolvedConf(ctx, h, []string{"1.1.1.1"}), "failed to parse")
}

type md5Host struct {
	fsHost
	out []byte
	err error
}

func (m *md5Host) Execute(context.Context, string, ...string) ([]byte, error) { return m.out, m.err }

func TestTheSameTauIsNotUploadedAgain(t *testing.T) {
	d := &sporedrive{tauBinaryHash: "abc"}
	ctx := context.Background()
	assert.Assert(t, d.isSameTau(ctx, &md5Host{out: []byte("abc */tb/bin/tau\n")}))
	assert.Assert(t, !d.isSameTau(ctx, &md5Host{out: []byte("def */tb/bin/tau\n")}))
	assert.Assert(t, !d.isSameTau(ctx, &md5Host{err: errors.New("no file")}))
	assert.Assert(t, !d.isSameTau(ctx, &md5Host{out: []byte("abc")}))
}

func TestOnlyResolversThatAnswerAreKept(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, len(probeDNSResolvers(ctx, &md5Host{err: errors.New("no dig")})), 0)
	assert.Equal(t, len(probeDNSResolvers(ctx, &md5Host{out: []byte("not an address\n")})), 0)
	assert.DeepEqual(t, probeDNSResolvers(ctx, &md5Host{out: []byte("142.250.0.1\n")}), dnsResolvers)
}

func TestADownloadThatIsNotARelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			io.WriteString(w, "not gzip")
		case "/other":
			w.Write(tarball(t, "README", []byte("hi")))
		case "/truncated":
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			gz.Write([]byte("definitely not a tar header"))
			gz.Close()
			w.Write(buf.Bytes())
		}
	}))
	defer srv.Close()
	_, err := httpDownload(srv.URL + "/plain")
	assert.ErrorContains(t, err, "gzip")
	_, err = httpDownload(srv.URL + "/other")
	assert.ErrorContains(t, err, "not found in archive")
	_, err = httpDownload(srv.URL + "/truncated")
	assert.ErrorContains(t, err, "tar")
	_, err = httpDownload("http://127.0.0.1:1/unreachable")
	assert.ErrorContains(t, err, "client do")
	_, err = httpDownload("://bad")
	assert.ErrorContains(t, err, "new http request")
}

func TestUploadsReportAHostThatCannotBeWritten(t *testing.T) {
	ro := &fsHost{fs: afero.NewReadOnlyFs(afero.NewMemMapFs())}
	d := &sporedrive{tauBinary: []byte("tau"), now: time.Now}
	_, _, err := d.uploadTau(context.Background(), ro, "/tb/bin/tau")
	assert.ErrorContains(t, err, "failed to open")
	assert.ErrorContains(t, d.uploadConfig(ro, unit{name: "node"}), "failed to open")
}
