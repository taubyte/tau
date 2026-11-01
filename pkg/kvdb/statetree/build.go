package statetree

import (
	"bytes"
	"context"
	"iter"

	ipld "github.com/ipfs/go-ipld-format"
)

// A frame is one node Build has open: its entries packed in buf at offs (key
// start, key end and value end each) and the subtree before each. The subtree
// after the last is still being built.
type frame struct {
	level uint32
	buf   []byte
	offs  []int
	kids  []ref
}

func (f *frame) entry(i int) entry {
	s, k, v := f.offs[3*i], f.offs[3*i+1], f.offs[3*i+2]
	e := entry{key: f.buf[s:k:k], val: f.buf[k:v:v]}
	if len(e.val) == 0 {
		e.val = nil
	}
	return e
}

func (f *frame) add(key, value []byte, before ref) {
	s := len(f.buf)
	f.buf = append(f.buf, key...)
	k := len(f.buf)
	f.buf = append(f.buf, value...)
	f.offs = append(f.offs, s, k, len(f.buf))
	f.kids = append(f.kids, before)
}

const buildFlush = 1024

type builder struct {
	o       op
	frames  []frame
	top     int
	entries []entry
	carry   ref
}

// close finishes the top frame with last as its final subtree; it becomes the
// carry for the frame below.
func (b *builder) close(last ref) error {
	f := &b.frames[b.top-1]
	f.kids = append(f.kids, last)
	b.entries = b.entries[:0]
	for i := range len(f.offs) / 3 {
		b.entries = append(b.entries, f.entry(i))
	}
	r, err := b.o.write(&node{level: f.level, entries: b.entries, kids: f.kids})
	if err != nil {
		return err
	}
	clear(b.entries)
	f.buf, f.offs, f.kids = f.buf[:0], f.offs[:0], f.kids[:0]
	b.top--
	b.carry = r
	if len(b.o.out) >= buildFlush {
		return b.o.flush()
	}
	return nil
}

func (b *builder) push(lvl uint32) *frame {
	if b.top == len(b.frames) {
		b.frames = append(b.frames, frame{})
	}
	b.top++
	f := &b.frames[b.top-1]
	f.level = lvl
	return f
}

// Build makes the tree of seq, whose keys must be strictly ascending
// (ErrDuplicateKey, ErrUnsorted otherwise). It writes each node once, and the
// tree is the one any order of Puts gives.
func Build(ctx context.Context, dags ipld.DAGService, seq iter.Seq2[[]byte, []byte], opts ...Option) (*Tree, error) {
	t := New(dags, opts...)
	b := &builder{o: op{ctx: ctx, t: t}}
	var prev []byte
	first := true
	for key, value := range seq {
		if !first {
			switch bytes.Compare(prev, key) {
			case 0:
				return nil, ErrDuplicateKey
			case 1:
				return nil, ErrUnsorted
			}
		}
		first = false
		prev = append(prev[:0], key...)
		lvl := Level(key)
		for b.top > 0 && b.frames[b.top-1].level < lvl {
			if err := b.close(b.carry); err != nil {
				return nil, err
			}
		}
		f := (*frame)(nil)
		if b.top > 0 && b.frames[b.top-1].level == lvl {
			f = &b.frames[b.top-1]
		} else {
			f = b.push(lvl)
		}
		f.add(key, value, b.carry)
		b.carry = ref{}
	}
	for b.top > 0 {
		if err := b.close(b.carry); err != nil {
			return nil, err
		}
	}
	if err := b.o.flush(); err != nil {
		return nil, err
	}
	return t.with(b.carry.c), nil
}
