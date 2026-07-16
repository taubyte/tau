package fifo

import (
	"container/list"
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *Factory) generateFifoId() uint32 {
	f.fifoLock.Lock()
	f.idsToGrab += 1
	f.fifoLock.Unlock()
	return f.idsToGrab
}

func (f *Factory) getFifo(fifoId uint32) (*Fifo, errno.Error) {
	f.fifoLock.RLock()
	defer f.fifoLock.RUnlock()
	if fifo, ok := f.fifoMap[fifoId]; ok {
		return fifo, 0
	}

	return nil, errno.ErrorFifoNotFound
}

func (f *Factory) fifoNew(
	ctx context.Context,
	module vm.Module,
	readCloser uint32,
) uint32 {
	fifo := Fifo{
		readCloser: readCloser == 1,
		list:       list.New(),
		id:         f.generateFifoId(),
	}

	f.fifoLock.Lock()
	f.fifoMap[fifo.id] = &fifo
	f.fifoLock.Unlock()

	return fifo.id
}

func (f *Factory) fifoPush(
	ctx context.Context,
	module vm.Module,
	id,
	buf uint32,
) uint32 {
	ff, err := f.getFifo(id)
	if err != 0 {
		return uint32(err)
	}

	ff.list.PushBack(byte(buf))
	return uint32(0)
}

func (f *Factory) fifoPop(
	ctx context.Context,
	module vm.Module,
	id,
	bufPtr uint32,
) uint32 {
	ff, err0 := f.getFifo(id)
	if err0 != 0 {
		return uint32(err0)
	}

	bufIface := ff.list.Front()
	if bufIface != nil {
		ff.list.Remove(bufIface)

		buf, ok := bufIface.Value.(byte)
		if !ok {
			return uint32(errno.ErrorFifoDatatypeInvalid)
		}

		return uint32(f.WriteByte(module, bufPtr, buf))
	}

	return uint32(errno.ErrorEOF)
}

func (f *Factory) fifoIsCloser(
	ctx context.Context,
	module vm.Module,
	id,
	isCloser uint32,
) uint32 {
	ff, err0 := f.getFifo(id)
	if err0 != 0 {
		return uint32(err0)
	}

	return uint32(f.WriteBool(module, isCloser, ff.readCloser))
}

func (f *Factory) fifoClose(
	ctx context.Context,
	module vm.Module,
	id uint32,
) {
	f.fifoLock.Lock()
	delete(f.fifoMap, id)
	f.fifoLock.Unlock()
}
