package tns

import (
	"strings"
	"time"

	"github.com/taubyte/tau/clients/p2p/tns/common"
	"github.com/taubyte/tau/services/tns/engine"
)

// notifyDelay coalesces the many keys of one push into one notice per channel.
var notifyDelay = 100 * time.Millisecond

// notify publishes, from this node, the /tns/updates/ notices a change to the
// store key concerns: the same empty ones a writer publishes after a push.
func (srv *Service) notify(key string) {
	path := strings.Split(strings.Trim(key, "/"), "/")
	if len(path) <= len(engine.Prefix) {
		return
	}

	srv.pendingLock.Lock()
	defer srv.pendingLock.Unlock()
	if srv.pending == nil {
		srv.pending = make(map[string]bool)
		time.AfterFunc(notifyDelay, srv.flushNotices)
	}
	for _, ch := range common.ChannelsFor(path[len(engine.Prefix):]...) {
		srv.pending[ch] = true
	}
}

func (srv *Service) flushNotices() {
	srv.pendingLock.Lock()
	channels := srv.pending
	srv.pending = nil
	srv.pendingLock.Unlock()

	ctx := srv.node.Context()
	for ch := range channels {
		if ctx.Err() != nil {
			return
		}
		if err := srv.node.PubSubPublish(ctx, ch, nil); err != nil {
			logger.Debugf("notice on %s failed: %s", ch, err)
		}
	}
}
