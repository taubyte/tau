package memory

import (
	"context"
	"io"

	"github.com/taubyte/tau/core/vm"
)

type WasmMemoryReader struct {
	ctx    context.Context
	mem    vm.Memory
	offset uint32
	cur    uint32
	size   uint32
	closed bool
}

func (wr *WasmMemoryReader) Read(p []byte) (n int, err error) {
	if wr.closed || wr.cur >= wr.size {
		return 0, io.EOF
	}

	size := len(p)
	if size > int(wr.size-wr.cur) {
		size = int(wr.size - wr.cur)
	}
	tbuf, ok := wr.mem.Read(wr.offset+wr.cur, uint32(size))
	if !ok {
		return 0, io.EOF
	}

	wr.cur += uint32(len(tbuf))
	copy(p, tbuf)
	return len(tbuf), nil
}

func (wr *WasmMemoryReader) Close() error {
	wr.closed = true

	return nil
}

func New(ctx context.Context, mem vm.Memory, offset uint32, size uint32) *WasmMemoryReader {
	return &WasmMemoryReader{
		ctx:    ctx,
		mem:    mem,
		offset: offset,
		size:   size,
	}
}
