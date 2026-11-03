// Package site serves an assets package's built files: the gzipped tar the build
// stores, unpacked once into a directory and answered from it.
//
//	static  an unmatched path returns the not-found document with 404
//	spa     an unmatched path returns the index document with 200, for the page's
//	        own router
//
// The author chooses; this never guesses from whether a not-found document exists.
package site

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// The modes, as the vocabulary spells them.
const (
	ModeStatic = specs.GatewayRouteHandlerModeStatic
	ModeSPA    = specs.GatewayRouteHandlerModeSpa
)

// DefaultIndex is what a directory returns when the route names no index.
const DefaultIndex = "index.html"

// MaxSize bounds what one artifact may unpack to: the tar is a stranger's bytes and a
// few kilobytes can name terabytes.
const MaxSize = 2 << 30

// ErrTooLarge is an artifact unpacking past its bound.
var ErrTooLarge = errors.New("site: the artifact is larger than it may unpack to")

// Files is one artifact, unpacked.
type Files struct {
	root string
	size int64
}

// Unpack writes a gzipped tar into root, which it then owns, refusing past limit bytes.
func Unpack(content io.Reader, root string, limit int64) (*Files, error) {
	f := &Files{root: root}
	if err := f.unpack(content, limit); err != nil {
		return nil, err
	}
	return f, nil
}

// Size is what the unpacked files take, in bytes.
func (f *Files) Size() int64 { return f.size }

// Close removes the files.
func (f *Files) Close() error { return os.RemoveAll(f.root) }

// Serving is how a route serves files: the mode, and the index and not-found
// documents.
type Serving struct {
	Mode, Index, NotFound string
}

// Serve answers one request out of the files.
func (f *Files) Serve(s Serving, method, request string) *invoke.Response {
	// Files answer reads only; a 200 with the index for a POST would hide a broken
	// form.
	switch method {
	case "", http.MethodGet, http.MethodHead:
	default:
		return &invoke.Response{
			Status:  http.StatusMethodNotAllowed,
			Headers: map[string][]string{"Allow": {"GET, HEAD"}},
		}
	}

	index := s.Index
	if index == "" {
		index = DefaultIndex
	}
	if body, name, ok := f.read(request, index); ok {
		return answer(http.StatusOK, name, body, method)
	}

	if s.Mode == ModeSPA {
		if body, name, ok := f.read(index, index); ok {
			return answer(http.StatusOK, name, body, method)
		}
	}

	if s.NotFound != "" {
		if body, name, ok := f.read(s.NotFound, index); ok {
			return answer(http.StatusNotFound, name, body, method)
		}
	}
	return &invoke.Response{Status: http.StatusNotFound}
}

// read resolves a request path to a file.
func (f *Files) read(request, index string) (body []byte, name string, ok bool) {
	// Cleaned as an absolute path first so every `..` resolves before the join, as
	// net/http's file server does.
	cleaned := path.Clean("/" + strings.TrimPrefix(request, "/"))
	full := filepath.Join(f.root, filepath.FromSlash(cleaned))

	info, err := os.Stat(full)
	if err != nil {
		return nil, "", false
	}

	// A directory is its index, never a listing.
	if info.IsDir() {
		full = filepath.Join(full, index)
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

func (f *Files) unpack(content io.Reader, limit int64) error {
	gz, err := gzip.NewReader(content)
	if err != nil {
		return err
	}
	defer gz.Close()

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
		target, ok := within(f.root, header.Name)
		if !ok {
			return fmt.Errorf("site: %q climbs out of the artifact", header.Name)
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
			n, err := writeFile(target, archive, limit-f.size)
			f.size += n
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
		return 0, ErrTooLarge
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
		return n, ErrTooLarge
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
