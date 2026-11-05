package artifact

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A bundle carries a developer's build: the tree and each recipe's product keyed
// by digest. A cloud taking one finds each product by the digest it planned, so a
// product from a different recipe is never served.

const (
	bundleTree     = "tree/"
	bundleProducts = "products/"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ErrBadBundle is a bundle that is not one.
var ErrBadBundle = errors.New("artifact: not a bundle")

// Bundle is an unpacked bundle: the tree, and a file per recipe digest.
type Bundle struct {
	Tree     string
	Products map[string]string
}

// Product opens what the recipe built, by its digest.
func (b Bundle) Product(digest string) (io.ReadCloser, bool) {
	file, ok := b.Products[digest]
	if !ok {
		return nil, false
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	return f, true
}

// PackBundle writes files of tree, named relative to it, and each product by
// its recipe's digest.
func PackBundle(w io.Writer, tree string, files []string, products map[string]string) error {
	gz := gzip.NewWriter(w)
	archive := tar.NewWriter(gz)

	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if err := packFile(archive, bundleTree+filepath.ToSlash(name), filepath.Join(tree, name)); err != nil {
			return err
		}
	}

	digests := make([]string, 0, len(products))
	for digest := range products {
		if !digestPattern.MatchString(digest) {
			return fmt.Errorf("%w: %q is not a recipe digest", ErrBadBundle, digest)
		}
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	for _, digest := range digests {
		if err := packFile(archive, bundleProducts+digest, products[digest]); err != nil {
			return err
		}
	}

	if err := archive.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func packFile(archive *tar.Writer, name, from string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(archive, f)
	return err
}

// UnpackBundle reads a bundle into dir. Only regular files under tree/ and digests
// under products/ are written; links, devices and climbing names are refused.
func UnpackBundle(r io.Reader, dir string) (Bundle, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Bundle{}, fmt.Errorf("%w: %v", ErrBadBundle, err)
	}
	defer gz.Close()

	b := Bundle{Tree: filepath.Join(dir, "tree"), Products: map[string]string{}}
	if err := os.MkdirAll(b.Tree, 0o755); err != nil {
		return Bundle{}, err
	}
	products := filepath.Join(dir, "products")
	if err := os.MkdirAll(products, 0o755); err != nil {
		return Bundle{}, err
	}

	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return b, nil
		}
		if err != nil {
			return Bundle{}, fmt.Errorf("%w: %v", ErrBadBundle, err)
		}
		if header.Typeflag != tar.TypeReg {
			return Bundle{}, fmt.Errorf("%w: %q is not a regular file", ErrBadBundle, header.Name)
		}

		var to string
		switch {
		case strings.HasPrefix(header.Name, bundleTree):
			rel := path.Clean(strings.TrimPrefix(header.Name, bundleTree))
			if rel == "." || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
				return Bundle{}, fmt.Errorf("%w: %q leaves the tree", ErrBadBundle, header.Name)
			}
			to = filepath.Join(b.Tree, filepath.FromSlash(rel))
		case strings.HasPrefix(header.Name, bundleProducts):
			digest := strings.TrimPrefix(header.Name, bundleProducts)
			if !digestPattern.MatchString(digest) {
				return Bundle{}, fmt.Errorf("%w: %q is not a recipe digest", ErrBadBundle, header.Name)
			}
			to = filepath.Join(products, digest)
			b.Products[digest] = to
		default:
			return Bundle{}, fmt.Errorf("%w: %q is neither tree nor product", ErrBadBundle, header.Name)
		}

		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return Bundle{}, err
		}
		f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode).Perm()|0o600)
		if err != nil {
			return Bundle{}, err
		}
		_, err = io.Copy(f, archive)
		f.Close()
		if err != nil {
			return Bundle{}, fmt.Errorf("%w: %v", ErrBadBundle, err)
		}
	}
}
