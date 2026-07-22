package session

import (
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/spf13/afero"
)

// CoW is a copy-on-write filesystem that, unlike afero.CopyOnWriteFs, can delete
// files that live only in the read-only base. Writes and copy-up are inherited
// from the embedded *afero.CopyOnWriteFs; deletes are recorded as in-memory
// whiteouts and the read side (Stat/Open/Readdir) hides whiteouted paths, so a
// removed base file truly disappears. The overlay files plus the whiteout set are
// exactly the changeset over the base — cheap to inspect and to collapse on merge.
//
// afero.CopyOnWriteFs.Remove returns EPERM for base-only files (it has no
// whiteouts); this type exists to close that gap.
type CoW struct {
	afero.Fs // = afero.NewCopyOnWriteFs(base, layer): Create/OpenFile/Mkdir/Rename/copy-up

	base    afero.Fs
	layer   afero.Fs
	deleted map[string]bool // whiteouts (exact paths and RemoveAll subtrees)
}

// NewCoW layers a fresh in-memory overlay over base (kept read-only).
func NewCoW(base afero.Fs) *CoW {
	layer := afero.NewMemMapFs()
	return &CoW{
		Fs:      afero.NewCopyOnWriteFs(afero.NewReadOnlyFs(base), layer),
		base:    base,
		layer:   layer,
		deleted: map[string]bool{},
	}
}

func norm(name string) string { return path.Clean("/" + strings.ReplaceAll(name, "\\", "/")) }

// hidden reports whether name is whiteouted, directly or under a removed subtree.
func (c *CoW) hidden(name string) bool {
	n := norm(name)
	for d := range c.deleted {
		if n == d || strings.HasPrefix(n, d+"/") {
			return true
		}
	}
	return false
}

// unhide clears any whiteout covering name (a write re-creates the path).
func (c *CoW) unhide(name string) {
	n := norm(name)
	for d := range c.deleted {
		if n == d || strings.HasPrefix(n, d+"/") {
			delete(c.deleted, d)
		}
	}
}

func enoent(op, name string) error { return &os.PathError{Op: op, Path: name, Err: syscall.ENOENT} }

// Changed returns the changeset over the base: paths written in the overlay and
// paths whiteouted (deleted). Used to collapse a fork onto its parent on merge.
func (c *CoW) Changed() (written []string, deleted []string) {
	_ = afero.Walk(c.layer, "/", func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			written = append(written, norm(p))
		}
		return nil
	})
	for d := range c.deleted {
		deleted = append(deleted, d)
	}
	return written, deleted
}

func (c *CoW) Remove(name string) error {
	if c.hidden(name) {
		return enoent("remove", name)
	}
	if _, err := c.Fs.Stat(name); err != nil {
		return err
	}
	_ = c.layer.Remove(name) // drop any copy-up
	c.deleted[norm(name)] = true
	return nil
}

func (c *CoW) RemoveAll(name string) error {
	if c.hidden(name) {
		return nil
	}
	_ = c.layer.RemoveAll(name)
	c.deleted[norm(name)] = true // whiteout the whole subtree (see hidden)
	return nil
}

func (c *CoW) Stat(name string) (os.FileInfo, error) {
	if c.hidden(name) {
		return nil, enoent("stat", name)
	}
	return c.Fs.Stat(name)
}

func (c *CoW) Open(name string) (afero.File, error) {
	if c.hidden(name) {
		return nil, enoent("open", name)
	}
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &cowFile{File: f, cow: c, dir: norm(name)}, nil
}

func (c *CoW) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&os.O_CREATE != 0 {
		c.unhide(name)
	} else if c.hidden(name) {
		return nil, enoent("open", name)
	}
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &cowFile{File: f, cow: c, dir: norm(name)}, nil
}

func (c *CoW) Create(name string) (afero.File, error) {
	c.unhide(name)
	f, err := c.Fs.Create(name)
	if err != nil {
		return nil, err
	}
	return &cowFile{File: f, cow: c, dir: norm(name)}, nil
}

// cowFile filters whiteouted entries out of directory listings.
type cowFile struct {
	afero.File
	cow *CoW
	dir string
}

func (f *cowFile) Readdir(n int) ([]os.FileInfo, error) {
	infos, err := f.File.Readdir(n)
	out := infos[:0]
	for _, fi := range infos {
		if !f.cow.hidden(f.dir + "/" + fi.Name()) {
			out = append(out, fi)
		}
	}
	return out, err
}

func (f *cowFile) Readdirnames(n int) ([]string, error) {
	names, err := f.File.Readdirnames(n)
	out := names[:0]
	for _, name := range names {
		if !f.cow.hidden(f.dir + "/" + name) {
			out = append(out, name)
		}
	}
	return out, err
}
