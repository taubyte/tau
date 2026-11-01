package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// maxDockerfile is the largest Dockerfile a restricted build reads to check.
// It is read whole, and nothing written by hand comes near it.
const maxDockerfile = 1 << 20

// buildContext is a build context spooled to a file and re-encoded as the plain tar
// the daemon is sent. On disk, under the disk's reserve, since a compressed context
// says nothing in advance about its size. Re-encoded so what is checked is what is
// sent.
type buildContext struct {
	file  *os.File
	floor reserve.Floor
	out   *bufio.Writer
	tar   *tar.Writer

	// at is how much of the tar has been written.
	at int64

	// root is the context as the daemon leaves it unpacked.
	root *node
}

// spoolContext reads a build context the way the daemon unpacks it into a file
// under dir, the system's temporary directory when dir is empty.
//
// WHAT CANNOT BE READ THE SAME WAY IS REFUSED: an xz context, and an entry the
// daemon would refuse to unpack.
func spoolContext(context io.Reader, dir string, floor reserve.Floor) (*buildContext, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := floor.Take(dir, 0, 1); err != nil {
		return nil, fmt.Errorf("spooling build context: %w", err)
	}
	file, err := os.CreateTemp(dir, "tau-build-context-*")
	if err != nil {
		return nil, fmt.Errorf("spooling build context: %w", err)
	}

	spooled := &buildContext{
		file:  file,
		floor: floor,
		root:  &node{kind: tar.TypeDir, children: map[string]*node{}},
	}
	spooled.out = bufio.NewWriterSize(reserved{spooled}, reserve.Chunk)
	spooled.tar = tar.NewWriter(counted{spooled})
	if err := spooled.copy(context); err != nil {
		spooled.remove()
		return nil, fmt.Errorf("reading build context: %w", err)
	}
	return spooled, nil
}

func (c *buildContext) copy(context io.Reader) error {
	stream, done, err := decompressed(context)
	if err != nil {
		return err
	}
	defer done()

	reader := tar.NewReader(stream)
	buf := make([]byte, reserve.Chunk)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := c.put(header, reader, buf); err != nil {
			return err
		}
	}
}

// put appends an entry, which the daemon unpacks over whatever an earlier
// entry left at its name.
func (c *buildContext) put(header *tar.Header, content io.Reader, buf []byte) error {
	if err := c.tar.Flush(); err != nil {
		return err
	}
	at := c.at
	if err := c.tar.WriteHeader(header); err != nil {
		return err
	}
	if _, err := io.CopyBuffer(c.tar, content, buf); err != nil {
		return err
	}
	return c.root.place(header, at)
}

// dockerfile answers the Dockerfile the daemon builds out of the context, with
// the name it is unpacked at once every link on the way to it is followed; no
// header when there is none, which is the daemon's error to give.
//
// The daemon follows links inside the context both while it unpacks and when it
// opens the Dockerfile, and opens "dockerfile" when the default "Dockerfile" is
// not there; a Dockerfile that is not a regular file is refused.
func (c *buildContext) dockerfile(name string) (*tar.Header, []byte, string, error) {
	file, at, err := c.root.dockerfile(name)
	if err != nil || file == nil {
		return nil, nil, "", err
	}
	if err := c.out.Flush(); err != nil {
		return nil, nil, "", err
	}

	reader := tar.NewReader(io.NewSectionReader(c.file, file.at, math.MaxInt64))
	header, err := reader.Next()
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s from the build context: %w", name, err)
	}
	if header.Size > maxDockerfile {
		return nil, nil, "", fmt.Errorf("the Dockerfile %q is %d bytes, more than the %d a restricted build reads to check", name, header.Size, maxDockerfile)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s from the build context: %w", name, err)
	}
	return header, body, at, nil
}

// finish ends the tar and answers it from the start, for the daemon.
func (c *buildContext) finish() (io.Reader, error) {
	if err := c.tar.Close(); err != nil {
		return nil, err
	}
	if err := c.out.Flush(); err != nil {
		return nil, err
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// Not the file itself: the HTTP client closes a body it can, and the
	// spool is removed after the build answers, not when it is sent.
	return struct{ io.Reader }{c.file}, nil
}

func (c *buildContext) remove() {
	c.file.Close()
	os.Remove(c.file.Name())
}

// counted is what the tar writes through, so where each entry starts is known.
type counted struct{ c *buildContext }

func (w counted) Write(p []byte) (int, error) {
	n, err := w.c.out.Write(p)
	w.c.at += int64(n)
	return n, err
}

// reserved writes the spool a chunk at a time, each once the reserve has room.
type reserved struct{ c *buildContext }

func (w reserved) Write(p []byte) (int, error) {
	if err := w.c.floor.Take(w.c.file.Name(), uint64(len(p)), 0); err != nil {
		return 0, err
	}
	return w.c.file.Write(p)
}

// node is one name in a build context as the daemon leaves it unpacked.
type node struct {
	// kind is tar.TypeDir, tar.TypeReg or tar.TypeSymlink, and zero for a
	// device or a fifo, which nothing here reads.
	kind byte

	// link is where a symlink points.
	link string

	// at is where in the spool a regular file's entry starts. A hard link
	// shares it, so the bytes are the ones the target had when it was linked,
	// as they are on disk.
	at int64

	children map[string]*node
}

// place unpacks one entry's name into the tree under root.
func (root *node) place(header *tar.Header, at int64) error {
	if header.Typeflag == tar.TypeXGlobalHeader {
		return nil
	}
	name, err := local(header.Name)
	if err != nil || name == "." {
		return err
	}

	under, base := path.Split(name)
	parent, _, err := root.lookup(under, true)
	if err != nil {
		return err
	}
	if parent.kind != tar.TypeDir {
		return fmt.Errorf("%q is under something that is not a directory", header.Name)
	}

	placed := &node{kind: header.Typeflag}
	switch header.Typeflag {
	case tar.TypeDir:
		if existing := parent.children[base]; existing != nil && existing.kind == tar.TypeDir {
			return nil
		}
		placed.children = map[string]*node{}
	case tar.TypeReg:
		placed.at = at
	case tar.TypeSymlink:
		placed.link = header.Linkname
	case tar.TypeLink:
		if placed, err = root.linked(header.Linkname); err != nil {
			return err
		}
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		placed.kind = 0
	default:
		return fmt.Errorf("the daemon does not unpack %q, of type %q", header.Name, header.Typeflag)
	}
	parent.children[base] = placed
	return nil
}

// linked is what a hard link to target is: the entry target names, itself
// rather than where it leads when it is a link.
func (root *node) linked(target string) (*node, error) {
	name, err := local(target)
	if err != nil {
		return nil, err
	}
	under, base := path.Split(name)
	parent, _, err := root.lookup(under, false)
	if err != nil {
		return nil, err
	}
	var found *node
	if parent != nil && parent.kind == tar.TypeDir {
		found = parent.children[base]
	}
	if found == nil || found.kind == tar.TypeDir {
		return nil, fmt.Errorf("a hard link to %q, which is not a file in the build context", target)
	}
	linked := *found
	return &linked, nil
}

// dockerfile is the file the daemon opens as the Dockerfile at name and where
// it lies, nil when there is none.
func (root *node) dockerfile(name string) (*node, string, error) {
	clean := path.Clean("./" + name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, "", fmt.Errorf("the Dockerfile %q is outside the build context", name)
	}
	file, at, err := root.lookup(clean, false)
	if err == nil && file == nil && name == "Dockerfile" {
		file, at, err = root.lookup("dockerfile", false)
	}
	switch {
	case err != nil || file == nil:
		return nil, "", err
	case file.kind != tar.TypeReg:
		return nil, "", fmt.Errorf("the Dockerfile %q is not a regular file in the build context", name)
	}
	return file, at, nil
}

// lookupSteps is how many elements, links' included, the daemon follows in one
// name before it gives up on it.
const lookupSteps = 255

// lookup follows name through the tree the way the daemon resolves a path in
// a context, and answers what it reaches and the name it is at with no link
// left in it: each link is read where it sits, an absolute one from the root,
// and ".." stops at the root. A name that is not there answers nil, unless
// create is set, when each missing directory along it is made, which is how an
// entry's parent comes to exist.
func (root *node) lookup(name string, create bool) (*node, string, error) {
	at := []*node{root}
	var names []string
	rest := strings.Split(name, "/")
	for steps := 0; len(rest) > 0; steps++ {
		if steps > lookupSteps {
			return nil, "", fmt.Errorf("%q passes through too many links", name)
		}
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(at) > 1 {
				at = at[:len(at)-1]
				names = names[:len(names)-1]
			}
			continue
		}

		dir := at[len(at)-1]
		if dir == nil {
			at = append(at, nil)
			names = append(names, part)
			continue
		}
		if dir.kind != tar.TypeDir {
			return nil, "", fmt.Errorf("%q passes through something that is not a directory", name)
		}
		child := dir.children[part]
		switch {
		case child == nil && create:
			child = &node{kind: tar.TypeDir, children: map[string]*node{}}
			dir.children[part] = child
		case child != nil && child.kind == tar.TypeSymlink:
			if path.IsAbs(child.link) {
				at = at[:1]
				names = names[:0]
			}
			rest = append(strings.Split(child.link, "/"), rest...)
			continue
		}
		at = append(at, child)
		names = append(names, part)
	}
	return at[len(at)-1], path.Join(names...), nil
}

// local is an entry's name relative to the root of the context, the way the
// daemon reads one: a leading "/" is the root, and a name that climbs out of
// it is refused.
func local(name string) (string, error) {
	clean := path.Clean(strings.TrimLeft(name, "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q is outside the build context", name)
	}
	return clean, nil
}

// maxZstdWindow is the most history a zstd context may ask to be held while it
// is decompressed: 128MiB, the zstd tool's own default limit, past which it
// refuses a frame unless told otherwise.
const maxZstdWindow = 1 << 27

// decompressed is the tar inside a context, recognised by the magic numbers the
// daemon tests for, in the order it tests them.
func decompressed(context io.Reader) (io.Reader, func(), error) {
	raw := bufio.NewReader(context)
	magic, err := raw.Peek(8)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	switch {
	case bytes.HasPrefix(magic, []byte("BZh")):
		return bzip2.NewReader(raw), func() {}, nil
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b, 0x08}):
		gz, err := gzip.NewReader(raw)
		if err != nil {
			return nil, nil, err
		}
		return gz, func() { gz.Close() }, nil
	case bytes.HasPrefix(magic, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return nil, nil, errors.New("an xz-compressed build context cannot be read here; send it uncompressed or gzip, bzip2 or zstd compressed")
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}),
		len(magic) >= 8 && binary.LittleEndian.Uint32(magic)&0xfffffff0 == 0x184d2a50:
		zs, err := zstd.NewReader(raw, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(maxZstdWindow))
		if err != nil {
			return nil, nil, err
		}
		return zs, zs.Close, nil
	}
	return raw, func() {}, nil
}
