package memoryView

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) generateMemoryViewId() uint32 {
	f.mvLock.Lock()
	f.idsToGrab += 1
	f.mvLock.Unlock()
	return f.idsToGrab
}

func (f *Factory) getMemoryView(viewId uint32) (*MemoryView, errno.Error) {
	f.mvLock.RLock()
	defer f.mvLock.RUnlock()
	if memoryView, ok := f.memoryViews[viewId]; ok {
		return memoryView, 0
	}

	return nil, errno.ErrorMemoryViewNotFound
}

func (f *Factory) W_memoryViewNew(
	ctx context.Context,
	module common.Module,
	bufPtr,
	size,
	isCloser,
	idPtr uint32,
) errno.Error {
	closable, err := f.ReadBool(module, isCloser)
	if err != 0 {
		return err
	}

	view := MemoryView{
		closable: closable,
		size:     size,
		bufPtr:   bufPtr,
		module:   module,
		id:       f.generateMemoryViewId(),
	}

	f.mvLock.Lock()
	f.memoryViews[view.id] = &view
	f.mvLock.Unlock()

	return f.WriteUint32Le(module, idPtr, view.id)
}

func (f *Factory) W_memoryViewOpen(
	ctx context.Context,
	module common.Module,
	id,
	isClosablePtr,
	sizePtr uint32,
) (error errno.Error) {
	if mv, err := f.getMemoryView(id); err != 0 {
		return err
	} else {
		if err = f.WriteUint32Le(module, sizePtr, mv.size); err != 0 {
			return err
		}

		return f.WriteBool(module, isClosablePtr, mv.closable)
	}
}

func (f *Factory) W_memoryViewRead(
	ctx context.Context,
	module common.Module,
	id,
	offset,
	count,
	bufPtr,
	nPtr uint32,
) errno.Error {
	mv, err0 := f.getMemoryView(id)
	if err0 != 0 {
		return err0
	}

	if offset >= mv.size {
		return errno.ErrorAddressOutOfMemory
	}

	data, err0 := f.ReadBytes(mv.module, mv.bufPtr, mv.size)
	if err0 != 0 {
		return err0
	}

	size := mv.size
	if size < offset+count {
		count = size - offset
	}

	if err0 = f.WriteBytes(module, bufPtr, data[offset:offset+count]); err0 != 0 {
		return err0
	}

	if err0 = f.WriteUint32Le(module, nPtr, count); err0 != 0 {
		return err0
	}

	return 0
}

func (f *Factory) W_memoryViewClose(
	ctx context.Context,
	module common.Module,
	id uint32,
) {
	f.mvLock.Lock()
	delete(f.memoryViews, id)
	f.mvLock.Unlock()
}
