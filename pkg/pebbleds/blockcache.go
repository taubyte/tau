package pebbleds

import (
	"encoding/binary"
	"errors"
	"hash/maphash"
	"sync"
	"sync/atomic"
)

// Has, the call bitswap makes for every block a peer wants, rarely reads the
// store: a bloom filter of every block held answers most blocks not held, and
// a table of recent blocks the ones held lately. Neither allocates. The filter
// is built by one scan of the block keys and answers nothing until done.

// A filter holding a key per bloomLoad bits is rebuilt twice the size.
const (
	bloomLoad   = 10
	bloomProbes = 7
)

var bloomBits uint64 = 1 << 24

var bloomSeed = maphash.MakeSeed()

type blockBloom struct {
	words []atomic.Uint64
	mask  uint64
	added atomic.Int64
}

func newBlockBloom(bits uint64) *blockBloom {
	return &blockBloom{words: make([]atomic.Uint64, bits/64), mask: bits - 1}
}

func (f *blockBloom) add(h []byte) {
	x := maphash.Bytes(bloomSeed, h)
	step := x>>33 | 1
	for range bloomProbes {
		p := x & f.mask
		f.words[p>>6].Or(1 << (p & 63))
		x += step
	}
	f.added.Add(1)
}

func (f *blockBloom) mayHave(h []byte) bool {
	x := maphash.Bytes(bloomSeed, h)
	step := x>>33 | 1
	for range bloomProbes {
		p := x & f.mask
		if f.words[p>>6].Load()&(1<<(p&63)) == 0 {
			return false
		}
		x += step
	}
	return true
}

// The table of recent blocks has hitSets sets of hitWays digests: 512 KiB.
const (
	hitSets = 1 << 12
	hitWays = 4
)

// A hitTable holds the sha2-256 digests of blocks held lately, a block's
// in one of the ways of the set its digest picks. A way is read and written
// a word at a time, so a read can see part of one digest and part of
// another; such a mix matches no digest but those two, and both were held.
type hitTable struct {
	sets [hitSets]hitSet
}

type hitSet struct {
	ways [hitWays][4]atomic.Uint64
	next atomic.Uint32
}

func digestOf(h []byte) ([]byte, bool) {
	if len(h) != 34 || h[0] != 0x12 || h[1] != 32 {
		return nil, false
	}
	return h[2:], true
}

func (t *hitTable) set(d []byte) *hitSet {
	return &t.sets[binary.LittleEndian.Uint64(d)&(hitSets-1)]
}

func holds(w *[4]atomic.Uint64, d []byte) bool {
	for i := range w {
		if w[i].Load() != binary.LittleEndian.Uint64(d[8*i:]) {
			return false
		}
	}
	return true
}

func (s *hitSet) way(d []byte) int {
	for i := range s.ways {
		if holds(&s.ways[i], d) {
			return i
		}
	}
	return -1
}

func (t *hitTable) has(h []byte) bool {
	d, ok := digestOf(h)
	return ok && t.set(d).way(d) >= 0
}

func (t *hitTable) add(h []byte) {
	d, ok := digestOf(h)
	if !ok {
		return
	}
	s := t.set(d)
	if s.way(d) >= 0 {
		return
	}
	w := &s.ways[s.next.Add(1)%hitWays]
	for i := range w {
		w[i].Store(binary.LittleEndian.Uint64(d[8*i:]))
	}
}

func (t *hitTable) clear() {
	for i := range t.sets {
		for j := range t.sets[i].ways {
			for k := range t.sets[i].ways[j] {
				t.sets[i].ways[j][k].Store(0)
			}
		}
	}
}

// An empty way matches nothing: no digest is zero.
func (t *hitTable) drop(h []byte) {
	d, ok := digestOf(h)
	if !ok {
		return
	}
	s := t.set(d)
	if i := s.way(d); i >= 0 {
		for k := range s.ways[i] {
			s.ways[i][k].Store(0)
		}
	}
}

type blockMemory struct {
	hits hitTable
	// deletes counts block deletes, each once it is done, so a Has that
	// read a block before one and records it after sees that it moved.
	deletes atomic.Uint64
	// bloom answers once ready; next, while a rebuild runs, takes every
	// block added too.
	bloom, next atomic.Pointer[blockBloom]
	ready       atomic.Bool
	building    sync.Mutex
	once        sync.Once
}

func (m *blockMemory) start(s *Store) {
	m.once.Do(func() {
		m.bloom.Store(newBlockBloom(bloomBits))
		go m.build(s, m.bloom.Load(), true)
	})
}

// filter adds a block that is, or is about to be, written to the filter
// being built, then to the one answering: a rebuild makes the one it built
// answer before it stops building it, so the block is in that one either
// way.
func (m *blockMemory) filter(s *Store, h []byte) {
	f := m.bloom.Load()
	if f == nil {
		return
	}
	if n := m.next.Load(); n != nil {
		n.add(h)
	}
	f = m.bloom.Load()
	f.add(h)
	if m.ready.Load() && uint64(f.added.Load())*bloomLoad > f.mask+1 && m.building.TryLock() {
		if m.bloom.Load() != f || m.next.Load() != nil {
			m.building.Unlock()
			return
		}
		n := newBlockBloom(2 * (f.mask + 1))
		m.next.Store(n)
		m.building.Unlock()
		go m.build(s, n, false)
	}
}

// false only for a block that is not held.
func (m *blockMemory) mayHave(h []byte) bool {
	return !m.ready.Load() || m.bloom.Load().mayHave(h)
}

var errStopped = errors.New("pebbleds: store closing")

// build adds every block held to f, which every block written from now on
// is added to as well, then makes f the filter that answers. A block
// written after the scan began is one the scan may miss, and one added to
// f, since it was in place before the scan.
func (m *blockMemory) build(s *Store, f *blockBloom, first bool) {
	var h [128]byte
	err := s.Scan([]byte(blocksPrefix), func(k, _ []byte) error {
		if s.closed.Load() {
			return errStopped
		}
		n, err := keyEncoding.Decode(h[:], k[len(blocksPrefix):])
		if err == nil {
			f.add(h[:n])
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errStopped) && !errors.Is(err, ErrClosed) {
			logger.Errorf("building the bloom filter of blocks held: %v", err)
		}
		m.next.Store(nil)
		return
	}
	if first {
		m.ready.Store(true)
		return
	}
	m.building.Lock()
	m.bloom.Store(f)
	m.next.Store(nil)
	m.building.Unlock()
}
