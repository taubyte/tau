// Package reserve keeps a share of a node's filesystem free while bytes from
// somewhere else are written to it.
//
// A volume restored from a checkpoint and a build context unpacked for a
// builder are both written by the node, from bytes it did not choose, onto a
// disk everything else on the node shares. Neither has a size it can be held
// to in advance: a volume may have grown past its quota while it ran, which is
// allowed, and a compressed context says nothing about how much it unpacks to.
// What such a write must not do is take the filesystem down to nothing, because
// then every other writer on it fails — the node's own stores among them.
//
// So the bound is on the filesystem rather than on the write: a write never
// leaves less free than a share of the filesystem's space and of its inodes.
// The filesystem is read again before every chunk, so writers that run at the
// same time, and anything else filling the disk meanwhile, count against the
// same reserve. Each writer can overshoot it by at most one chunk.
package reserve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// Floor is the share of a filesystem a write leaves free: of its bytes, up to a
// cap when there is one, and of its inodes.
type Floor struct {
	Space  float64
	Most   uint64
	Inodes float64

	// Measure reads how full the filesystem holding path is. Nil reads the
	// filesystem itself.
	Measure func(path string) (Usage, error)
}

// Usage is a filesystem's size and what of it is free, in bytes and in inodes.
//
// A TOTAL OF ZERO IS ONE THE FILESYSTEM DOES NOT REPORT, and nothing of it is
// held back: btrfs, and a tmpfs without nr_inodes, make inodes as they need
// them and report no count, and a filesystem this cannot read at all reports
// neither.
type Usage struct {
	Size, Free       uint64
	Files, FreeFiles uint64
}

// Default is the reserve every write of this kind leaves on a node, which has
// no notion of a reserve of its own to use instead.
//
// ITS SPACE IS WHAT systemd-journald LEAVES FREE FOR EVERYTHING ELSE on the
// filesystem it writes to, SystemKeepFree's default: 15% of it, but never more
// than 4GiB. A share, because a small disk fills in a small number of bytes; and
// capped, because a share of a large disk is far more than anything else on it
// needs to keep working, and holding it back would refuse a restore on a disk
// with hundreds of gigabytes free.
//
// ITS INODES ARE KUBELET'S, the nodefs.inodesFree<5% at which it starts evicting
// from a node, since journald keeps none back.
var Default = Floor{Space: 0.15, Most: 4 << 30, Inodes: 0.05}

// Error is a write the reserve refused, with the numbers it refused it on.
type Error struct {
	Path string
	Unit string

	Free, Want, Reserve uint64
}

func (e *Error) Error() string {
	return fmt.Sprintf("reserve: the filesystem holding %s has %d %s free, and %d more would leave less than its reserve of %d",
		e.Path, e.Free, e.Unit, e.Want, e.Reserve)
}

// Take answers whether size more bytes and files more inodes can go onto the
// filesystem holding path without leaving less free than the floor. Only what
// is asked for is checked: writing into a file that exists takes no inode.
func (f Floor) Take(path string, size, files uint64) error {
	if size == 0 && files == 0 {
		return nil
	}
	measure := f.Measure
	if measure == nil {
		measure = measured
	}
	usage, err := measure(path)
	if err != nil {
		return fmt.Errorf("reserve: reading the filesystem holding %s: %w", path, err)
	}
	if err := within(path, "bytes", usage.Size, usage.Free, size, f.Space, f.Most); err != nil {
		return err
	}
	return within(path, "inodes", usage.Files, usage.FreeFiles, files, f.Inodes, 0)
}

func within(path, unit string, total, free, want uint64, share float64, most uint64) error {
	if total == 0 || want == 0 {
		return nil
	}
	reserve := uint64(share * float64(total))
	if most > 0 {
		reserve = min(reserve, most)
	}
	if free >= want && free-want >= reserve {
		return nil
	}
	return &Error{Path: path, Unit: unit, Free: free, Want: want, Reserve: reserve}
}

const (
	// Chunk is how much is written between two readings of the filesystem,
	// and so how far past the reserve each writer can go.
	Chunk = 1 << 20

	// block is the run of zeros written as a hole: a filesystem allocates
	// in blocks, and this is the size of most filesystems' block.
	block = 4 << 10
)

var zeros [block]byte

// Copy writes what src holds into file, an empty file just made, and answers
// how long it made it.
//
// A BLOCK OF ZEROS IS LEFT AS A HOLE rather than written, so what was sparse
// where it came from takes no more space here, and the reserve is only taken
// for the bytes that land on disk.
func (f Floor) Copy(file *os.File, src io.Reader) (int64, error) {
	buf := make([]byte, Chunk)
	var at int64
	for {
		n, err := fill(src, buf)
		if n > 0 {
			if err := f.write(file, buf[:n], at); err != nil {
				return at, err
			}
			at += int64(n)
		}
		if errors.Is(err, io.EOF) {
			return at, extend(file, at)
		}
		if err != nil {
			return at, err
		}
	}
}

// write puts one chunk at offset at, leaving out the blocks that are all
// zeros, once the reserve has room for the rest.
func (f Floor) write(file *os.File, chunk []byte, at int64) error {
	var (
		runs [][2]int
		data uint64
	)
	for off := 0; off < len(chunk); off += block {
		end := min(off+block, len(chunk))
		if bytes.Equal(chunk[off:end], zeros[:end-off]) {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1][1] == off {
			runs[n-1][1] = end
		} else {
			runs = append(runs, [2]int{off, end})
		}
		data += uint64(end - off)
	}
	if data == 0 {
		return nil
	}
	if err := f.Take(file.Name(), data, 0); err != nil {
		return err
	}
	for _, run := range runs {
		if _, err := file.WriteAt(chunk[run[0]:run[1]], at+int64(run[0])); err != nil {
			return err
		}
	}
	return nil
}

// extend gives a file ending in a hole the length no write gave it.
func extend(file *os.File, size int64) error {
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < size {
		return file.Truncate(size)
	}
	return nil
}

// fill reads until buf is full or src stops, and answers how much it read and
// why it stopped. An io.ErrUnexpectedEOF is then src's own error and never a
// short read, which io.ReadFull could not tell apart.
func fill(src io.Reader, buf []byte) (int, error) {
	var n int
	for n < len(buf) {
		m, err := src.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
