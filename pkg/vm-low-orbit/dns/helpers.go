package dns

import (
	"net"
	"sync"

	"github.com/taubyte/go-sdk/errno"
)

func (f *Factory) generateResolver() uint32 {
	f.resolversLock.Lock()
	defer func() {
		f.resolversIdToGrab += 1
		f.resolversLock.Unlock()
	}()

	f.resolvers[f.resolversIdToGrab] = &Resolver{
		Resolver:     &net.Resolver{},
		responseLock: sync.RWMutex{},
		response:     make(map[ResponseType]map[string][]string),
	}

	return f.resolversIdToGrab
}

func (f *Factory) getResolver(resolverId uint32) (*Resolver, errno.Error) {
	f.resolversLock.RLock()
	resolver, ok := f.resolvers[resolverId]
	f.resolversLock.RUnlock()
	if !ok {
		return nil, errno.ErrorResolverNotFound
	}

	return resolver, errno.ErrorNone
}

func (r *Resolver) cacheResponse(rtype ResponseType, name string, answer []string) {
	var newEntry map[string][]string
	r.responseLock.Lock()
	defer r.responseLock.Unlock()
	newEntry, ok := r.response[rtype]
	if !ok {
		newEntry = make(map[string][]string)
		r.response[rtype] = newEntry
	}

	newEntry[name] = answer
}

func (r *Resolver) getCachedResponse(rtype ResponseType, name string) ([]string, errno.Error) {
	r.responseLock.RLock()
	defer r.responseLock.RUnlock()
	_resp, ok := r.response[rtype]
	if !ok {
		return nil, errno.ErrorCachedResponseTypeNotFound
	}

	resp, ok := _resp[name]
	if !ok {
		return nil, errno.ErrorCachedResponseNotFound
	}

	delete(_resp, name)

	return resp, 0
}
