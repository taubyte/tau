package storage

import (
	"github.com/taubyte/go-sdk/errno"
)

func (f *Factory) getContent(contentId uint32) (*content, errno.Error) {
	f.contentLock.RLock()
	content, ok := f.contents[contentId]
	f.contentLock.RUnlock()
	if !ok {
		return nil, errno.ErrorContentNotFound
	}

	return content, errno.ErrorNone
}
