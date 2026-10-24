package docker

import (
	"archive/tar"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// remoteADD reports whether a Dockerfile asks the daemon to fetch a URL.
//
// ADD is the one instruction the daemon performs itself, in its own network
// namespace, rather than in a container. The egress firewall matches the network
// a restricted container runs on, so it does not and cannot cover a fetch the
// daemon makes on the build's behalf — the destination would be whatever the
// Dockerfile names. COPY does the same job for files that are in the context,
// which is where a build's own files are.
func remoteADD(dockerfile []byte) (string, bool) {
	for _, line := range instructions(dockerfile) {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "ADD") {
			continue
		}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "--") {
				continue // a flag, not a source
			}
			if strings.Contains(field, "://") {
				return field, true
			}
		}
	}
	return "", false
}

// instructions returns a Dockerfile's instruction lines, comments dropped and
// continuations joined, which is as much parsing as remoteADD needs.
func instructions(dockerfile []byte) []string {
	var (
		out     []string
		current strings.Builder
	)

	for _, raw := range strings.Split(string(dockerfile), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasSuffix(line, "\\") {
			current.WriteString(strings.TrimSuffix(line, "\\"))
			current.WriteString(" ")
			continue
		}

		current.WriteString(line)
		out = append(out, current.String())
		current.Reset()
	}

	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// readDockerfile returns the Dockerfile the daemon builds out of a build
// context, with the context's bytes so the caller can still hand them to it.
//
// IT READS THE CONTEXT THE WAY THE DAEMON UNPACKS IT, because the Dockerfile is
// read here only to be checked, and a check of any file but the one built
// checks nothing. The daemon decompresses the context, unpacks its entries in
// order so a later one replaces an earlier one of the same name, follows links
// inside the context both while it unpacks and when it opens the Dockerfile,
// and opens "dockerfile" when the default "Dockerfile" is not there.
//
// WHAT CANNOT BE READ THE SAME WAY IS REFUSED: an xz context, an entry the
// daemon would refuse to unpack, and a Dockerfile that is not a regular file.
// A context with no Dockerfile at all answers none, which is the daemon's error
// to give.
func readDockerfile(context io.Reader, name string) (dockerfile, buffered []byte, err error) {
	buffered, err = io.ReadAll(context)
	if err != nil {
		return nil, nil, fmt.Errorf("reading build context: %w", err)
	}

	root := &node{kind: tar.TypeDir, children: map[string]*node{}}
	err = eachEntry(buffered, func(index int, header *tar.Header, _ io.Reader) (bool, error) {
		return true, root.place(header, index)
	})
	if err != nil {
		return nil, buffered, fmt.Errorf("reading build context: %w", err)
	}

	file, err := root.dockerfile(name)
	if err != nil || file == nil {
		return nil, buffered, err
	}
	err = eachEntry(buffered, func(index int, _ *tar.Header, content io.Reader) (bool, error) {
		if index != file.entry {
			return true, nil
		}
		dockerfile, err = io.ReadAll(content)
		return false, err
	})
	if err != nil {
		return nil, buffered, fmt.Errorf("reading %s from the build context: %w", name, err)
	}
	return dockerfile, buffered, nil
}

// node is one name in a build context as the daemon leaves it unpacked.
type node struct {
	// kind is tar.TypeDir, tar.TypeReg or tar.TypeSymlink, and zero for a
	// device or a fifo, which nothing here reads.
	kind byte

	// link is where a symlink points.
	link string

	// entry is which entry of the context a regular file's bytes are. A hard
	// link shares it, so the bytes are the ones the target had when it was
	// linked, as they are on disk.
	entry int

	// children is a directory's.
	children map[string]*node
}

// place unpacks one entry's name into the tree under root.
func (root *node) place(header *tar.Header, index int) error {
	if header.Typeflag == tar.TypeXGlobalHeader {
		return nil
	}
	name, err := local(header.Name)
	if err != nil || name == "." {
		return err
	}

	under, base := path.Split(name)
	parent, err := root.lookup(under, true)
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
		placed.entry = index
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
	parent, err := root.lookup(under, false)
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

// dockerfile is the file the daemon opens as the Dockerfile at name, nil when
// there is none.
func (root *node) dockerfile(name string) (*node, error) {
	clean := path.Clean("./" + name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, fmt.Errorf("the Dockerfile %q is outside the build context", name)
	}
	file, err := root.lookup(clean, false)
	if err == nil && file == nil && name == "Dockerfile" {
		file, err = root.lookup("dockerfile", false)
	}
	switch {
	case err != nil || file == nil:
		return nil, err
	case file.kind != tar.TypeReg:
		return nil, fmt.Errorf("the Dockerfile %q is not a regular file in the build context", name)
	}
	return file, nil
}

// lookupSteps is how many elements, links' included, the daemon follows in one
// name before it gives up on it.
const lookupSteps = 255

// lookup follows name through the tree the way the daemon resolves a path in
// a context: each link is read where it sits, an absolute one from the root,
// and ".." stops at the root. A name that is not there answers nil, unless
// create is set, when each missing directory along it is made, which is how an
// entry's parent comes to exist.
func (root *node) lookup(name string, create bool) (*node, error) {
	at := []*node{root}
	rest := strings.Split(name, "/")
	for steps := 0; len(rest) > 0; steps++ {
		if steps > lookupSteps {
			return nil, fmt.Errorf("%q passes through too many links", name)
		}
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(at) > 1 {
				at = at[:len(at)-1]
			}
			continue
		}

		dir := at[len(at)-1]
		if dir == nil {
			at = append(at, nil)
			continue
		}
		if dir.kind != tar.TypeDir {
			return nil, fmt.Errorf("%q passes through something that is not a directory", name)
		}
		child := dir.children[part]
		switch {
		case child == nil && create:
			child = &node{kind: tar.TypeDir, children: map[string]*node{}}
			dir.children[part] = child
		case child != nil && child.kind == tar.TypeSymlink:
			if path.IsAbs(child.link) {
				at = at[:1]
			}
			rest = append(strings.Split(child.link, "/"), rest...)
			continue
		}
		at = append(at, child)
	}
	return at[len(at)-1], nil
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

// eachEntry calls each with the entries of the tar a context holds, in order,
// until it answers false.
func eachEntry(context []byte, each func(index int, header *tar.Header, content io.Reader) (bool, error)) error {
	stream, done, err := decompressed(context)
	if err != nil {
		return err
	}
	defer done()

	reader := tar.NewReader(stream)
	for index := 0; ; index++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if more, err := each(index, header, reader); err != nil || !more {
			return err
		}
	}
}

// decompressed is the tar inside a context, recognised by the magic numbers the
// daemon tests for, in the order it tests them.
func decompressed(context []byte) (io.Reader, func(), error) {
	raw := bytes.NewReader(context)
	switch {
	case bytes.HasPrefix(context, []byte("BZh")):
		return bzip2.NewReader(raw), func() {}, nil
	case bytes.HasPrefix(context, []byte{0x1f, 0x8b, 0x08}):
		gz, err := gzip.NewReader(raw)
		if err != nil {
			return nil, nil, err
		}
		return gz, func() { gz.Close() }, nil
	case bytes.HasPrefix(context, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return nil, nil, errors.New("an xz-compressed build context cannot be read here; send it uncompressed or gzip, bzip2 or zstd compressed")
	case bytes.HasPrefix(context, []byte{0x28, 0xb5, 0x2f, 0xfd}),
		len(context) >= 8 && binary.LittleEndian.Uint32(context)&0xfffffff0 == 0x184d2a50:
		zs, err := zstd.NewReader(raw)
		if err != nil {
			return nil, nil, err
		}
		return zs, zs.Close, nil
	}
	return raw, func() {}, nil
}
