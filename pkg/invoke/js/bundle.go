package js

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke/js/archive"
)

// bundle.go reads the archive an artifact is and serves its modules to a realm.
// Every module loads from source (warpjs cannot compile an ES module yet), but the
// reader still requires each `.warp` to ship beside its source.

// DefaultMaxArchive bounds an archive, packed and unpacked, unless WithMaxArchive
// says otherwise.
const DefaultMaxArchive = archive.DefaultMax

var (
	// ErrBadArchive is an artifact that is not an archive this tier can read.
	ErrBadArchive = errors.New("js: the artifact is not a function archive")

	// ErrArchiveFormat is an archive written in a layout this tier does not
	// know, which a newer node may.
	ErrArchiveFormat = errors.New("js: the archive is in a format this tier does not read")

	// ErrNoEntry is an archive whose manifest names an entry it does not hold.
	ErrNoEntry = errors.New("js: the archive does not hold its entry module")

	// ErrNoModule is an import of something the archive does not hold.
	ErrNoModule = errors.New("js: no such module in the archive")
)

type bundle struct {
	entry   string
	modules map[string]string
}

func readBundle(data []byte, max int64) (*bundle, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}

	members := map[string][]byte{}
	budget := max
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Name != archive.ManifestName {
			if err := archive.CheckName(f.Name); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
			}
		}
		if _, dup := members[f.Name]; dup {
			return nil, fmt.Errorf("%w: it holds %q twice", ErrBadArchive, f.Name)
		}
		body, err := readMember(f, &budget)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrBadArchive, f.Name, err)
		}
		members[f.Name] = body
	}

	raw, ok := members[archive.ManifestName]
	if !ok {
		return nil, fmt.Errorf("%w: it has no %s", ErrBadArchive, archive.ManifestName)
	}
	var m archive.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: its manifest: %v", ErrBadArchive, err)
	}
	if m.Format != archive.Format {
		return nil, fmt.Errorf("%w: format %d, and this tier reads %d", ErrArchiveFormat, m.Format, archive.Format)
	}

	b := &bundle{entry: m.Entry, modules: map[string]string{}}
	for name, body := range members {
		if source, ok := strings.CutSuffix(name, archive.CompiledExt); ok {
			if _, has := members[source]; !has {
				return nil, fmt.Errorf("%w: %v: %q has no %q", ErrBadArchive, archive.ErrOrphanCompile, name, source)
			}
		}
		if isModule(name) {
			b.modules[name] = string(body)
		}
	}
	if _, ok := b.modules[m.Entry]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoEntry, m.Entry)
	}
	return b, nil
}

// readMember reads one member within what is left of the budget. A member is
// refused on the size it declares, and archive/zip refuses one that inflates
// past its declaration, so nothing is read beyond the budget.
func readMember(f *zip.File, budget *int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(*budget) {
		return nil, errors.New("the archive unpacks to more than this tier reads")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	*budget -= int64(len(body))
	return body, nil
}

func isModule(name string) bool {
	return strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs")
}

func (b *bundle) host() engine.ModuleHost {
	return engine.ModuleHost{Resolve: b.resolve, Load: b.load}
}

// resolve finds a module in the archive: relative to the importer, or from the
// root for "/". A bare specifier, `node:` included, is refused as a TypeError.
func (b *bundle) resolve(req engine.ModuleRequest) (string, error) {
	if len(req.Attributes) > 0 {
		return "", fmt.Errorf("%w: %q is imported with attributes, which this tier does not serve", ErrNoModule, req.Specifier)
	}
	var name string
	switch spec := req.Specifier; {
	case req.Referrer == "":
		name = spec
	case strings.HasPrefix(spec, "./"), strings.HasPrefix(spec, "../"):
		name = path.Join(path.Dir(req.Referrer), spec)
	case strings.HasPrefix(spec, "/"):
		name = strings.TrimPrefix(path.Clean(spec), "/")
	default:
		return "", fmt.Errorf("%w: %q is a package or a built-in, and this tier serves only what the archive holds", ErrNoModule, spec)
	}
	if _, ok := b.modules[name]; !ok {
		return "", fmt.Errorf("%w: %q, imported from %q", ErrNoModule, req.Specifier, req.Referrer)
	}
	return name, nil
}

func (b *bundle) load(key string) (string, error) { return b.modules[key], nil }
