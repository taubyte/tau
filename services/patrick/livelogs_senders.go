package service

import (
	"context"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

// liveLogSenders accepts a job's live log only from the monkey the job is
// assigned to, the same rule a job's status report is held to. Any peer of the
// network could publish on the topic, and messages carry their signed sender.
//
// A sender once found to be the job's monkey is remembered for that job, so its
// last chunks still count when they arrive after Patrick has taken the job's
// report and dropped the assignment.
type liveLogSenders struct {
	lock     sync.Mutex
	verified map[string]peer.ID // job -> its monkey
}

const liveLogSendersMax = 4096

func (s *liveLogSenders) allowed(ctx context.Context, db kvdb.KVDB, jid string, from peer.ID) bool {
	if from == "" {
		return false
	}

	s.lock.Lock()
	known, ok := s.verified[jid]
	s.lock.Unlock()
	if ok && known == from {
		return true
	}

	// unknown, or another monkey: the job may have been handed to it
	data, err := db.Get(ctx, "/assigned/"+jid)
	if err != nil {
		return false
	}
	var assignment Assignment
	if cbor.Unmarshal(data, &assignment) != nil || assignment.MonkeyPID != from.String() {
		return false
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	if s.verified == nil || len(s.verified) >= liveLogSendersMax {
		s.verified = make(map[string]peer.ID)
	}
	s.verified[jid] = from
	return true
}
