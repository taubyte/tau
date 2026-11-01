// Package static serves a built site as a compute tier: fetching and unpacking is a
// cold start, disk the reason to evict, so the pool handles the lifecycle. The
// artifact is the gzipped tar the build stores (services/build/artifact).
//
//	static  an unmatched path returns the not-found document with 404
//	spa     an unmatched path returns the index document with 200, for the page's
//	        own router
//
// The author chooses; this never guesses from whether a not-found document exists.
package static

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/invoke"
)

// Name is the string the config vocabulary uses for this tier.
const Name = "static"

// Settings this tier reads off a Spec, spelled as the vocabulary spells them.
const (
	SettingMode     = specs.WebsiteServeKeyMode
	SettingIndex    = specs.WebsiteServeKeyIndex
	SettingNotFound = specs.WebsiteServeKeyNotFound

	ModeStatic = specs.WebsiteServeModeStatic
	ModeSPA    = specs.WebsiteServeModeSpa
)

// DefaultIndex is the vocabulary's own default for `serve/index`.
const DefaultIndex = "index.html"

// maxArtifact bounds what one site may unpack to: the tar is a stranger's bytes and
// a few kilobytes can name terabytes.
const maxArtifact = 2 << 30

var (
	ErrNoAssets   = errors.New("static: nothing to fetch a site from")
	ErrNoArtifact = errors.New("static: the spec names no artifact")
)

// Assets is where a built site is fetched from.
type Assets interface {
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// Runtime is the static tier.
type Runtime struct {
	assets Assets
}

var _ invoke.Runtime = (*Runtime)(nil)

// New builds one.
func New(assets Assets) (*Runtime, error) {
	if assets == nil {
		return nil, ErrNoAssets
	}
	return &Runtime{assets: assets}, nil
}

func (r *Runtime) Name() string { return Name }

// Start fetches a site and unpacks it. Its latency IS the cold start.
func (r *Runtime) Start(ctx context.Context, spec invoke.Spec) (invoke.Instance, error) {
	if spec.Artifact == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoArtifact, spec.Name)
	}

	content, err := r.assets.Get(ctx, spec.Artifact)
	if err != nil {
		return nil, fmt.Errorf("static: fetching %s: %w", spec.Artifact, err)
	}
	defer content.Close()

	// Its own directory, removed with it, so two revisions can be warm at once.
	root, err := os.MkdirTemp("", "tau-site-")
	if err != nil {
		return nil, fmt.Errorf("static: %w", err)
	}

	if err := unpack(content, root); err != nil {
		os.RemoveAll(root)
		return nil, fmt.Errorf("static: unpacking %s: %w", spec.Artifact, err)
	}

	return &site{
		root:     root,
		mode:     setting(spec, SettingMode, ModeStatic),
		index:    setting(spec, SettingIndex, DefaultIndex),
		notFound: setting(spec, SettingNotFound, ""),
	}, nil
}

func setting(spec invoke.Spec, key, fallback string) string {
	if v, ok := spec.Settings[key]; ok && v != "" {
		return v
	}
	return fallback
}

// site is one unpacked revision of one site.
type site struct {
	root     string
	mode     string
	index    string
	notFound string
}

func (s *site) Close() error { return os.RemoveAll(s.root) }

// Serve answers one request out of the unpacked directory.
func (s *site) Serve(_ context.Context, req *invoke.Request) (*invoke.Response, error) {
	// A site answers reads only; a 200 with the index for a POST would hide a broken
	// form.
	switch req.Method {
	case "", http.MethodGet, http.MethodHead:
	default:
		return &invoke.Response{
			Status:  http.StatusMethodNotAllowed,
			Headers: map[string][]string{"Allow": {"GET, HEAD"}},
		}, nil
	}

	if body, name, ok := s.read(req.Path); ok {
		return answer(http.StatusOK, name, body, req.Method), nil
	}

	// SPA: an unmatched path is the index with 200.
	if s.mode == ModeSPA {
		if body, name, ok := s.read(s.index); ok {
			return answer(http.StatusOK, name, body, req.Method), nil
		}
	}

	if s.notFound != "" {
		if body, name, ok := s.read(s.notFound); ok {
			return answer(http.StatusNotFound, name, body, req.Method), nil
		}
	}
	return &invoke.Response{Status: http.StatusNotFound}, nil
}

// read resolves a request path to a file in the site.
func (s *site) read(request string) (body []byte, name string, ok bool) {
	// Cleaned as an absolute path first so every `..` resolves before the join, as
	// net/http's file server does.
	cleaned := path.Clean("/" + strings.TrimPrefix(request, "/"))
	full := filepath.Join(s.root, filepath.FromSlash(cleaned))

	info, err := os.Stat(full)
	if err != nil {
		return nil, "", false
	}

	// A directory is its index, never a listing.
	if info.IsDir() {
		full = filepath.Join(full, s.index)
		if info, err = os.Stat(full); err != nil || info.IsDir() {
			return nil, "", false
		}
	}
	if !info.Mode().IsRegular() {
		return nil, "", false
	}

	body, err = os.ReadFile(full)
	if err != nil {
		return nil, "", false
	}
	return body, full, true
}

// answer builds a response, and a HEAD carries the headers without the body.
func answer(status int, name string, body []byte, method string) *invoke.Response {
	headers := map[string][]string{
		"Content-Type":           {contentType(name)},
		"X-Content-Type-Options": {"nosniff"},
	}
	if method == http.MethodHead {
		headers["Content-Length"] = []string{fmt.Sprint(len(body))}
		return &invoke.Response{Status: status, Headers: headers}
	}
	return &invoke.Response{Status: status, Headers: headers, Body: body}
}

// contentType is the extension's, octet-stream otherwise; it never sniffs, so an
// uploaded file cannot become a script on the site's origin.
func contentType(name string) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// unpack writes a gzipped tar into root.
func unpack(content io.Reader, root string) error {
	gz, err := gzip.NewReader(content)
	if err != nil {
		return err
	}
	defer gz.Close()

	var written int64
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		// An entry that climbs out is refused, not skipped: the archive was built to write
		// elsewhere.
		target, ok := within(root, header.Name)
		if !ok {
			return fmt.Errorf("static: %q climbs out of the site", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			n, err := writeFile(target, archive, maxArtifact-written)
			written += n
			if err != nil {
				return err
			}
		default:
			// A symlink is not unpacked.
			continue
		}
	}
}

func writeFile(target string, content io.Reader, room int64) (int64, error) {
	if room <= 0 {
		return 0, fmt.Errorf("static: the site is larger than %d bytes", int64(maxArtifact))
	}

	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Copy one byte past the room left, so a file exactly at the limit is told apart
	// from one beyond it.
	n, err := io.Copy(file, io.LimitReader(content, room+1))
	if err != nil {
		return n, err
	}
	if n > room {
		return n, fmt.Errorf("static: the site is larger than %d bytes", int64(maxArtifact))
	}
	return n, nil
}

// within joins a tar member's name under root and says whether it stayed.
func within(root, name string) (string, bool) {
	cleaned := filepath.Clean(filepath.Join(root, filepath.FromSlash(name)))
	rooted := filepath.Clean(root)
	if cleaned != rooted && !strings.HasPrefix(cleaned, rooted+string(filepath.Separator)) {
		return "", false
	}
	return cleaned, true
}
