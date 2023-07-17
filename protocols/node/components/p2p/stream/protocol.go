package stream

import (
	"errors"
	"fmt"
	"time"

	counter "bitbucket.org/taubyte/go-node-tvm/counter"
	"bitbucket.org/taubyte/go-node-tvm/lookup"
	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/substrate/p2p"
	"github.com/taubyte/utils/multihash"
)

func (st *Stream) ProtocolHash() (protocol string, err error) {
	if len(st.matcher.Project) == 0 {
		return "", errors.New("No project attached to stream")
	}

	if len(st.config.Id) == 0 {
		return "", errors.New("No id on attached service")
	}

	return multihash.Hash(st.matcher.Project+st.config.Id) + st.matcher.Protocol, nil
}

func (st *Stream) HandleRaw(cmd streams.Command) (resp streams.Response, err error) {
	start := time.Now()
	st.matcher.Command = cmd.Name()

	pickServiceables, err := lookup.Lookup(st.srv, st.matcher)
	if err != nil {
		return nil, fmt.Errorf("P2P serviceable lookup failed with: %s", err)
	}

	if len(pickServiceables) > 1 {
		return nil, fmt.Errorf("Unexpected multiple picks for given matcher %v", st.matcher)
	}

	pick, ok := pickServiceables[0].(iface.Serviceable)
	if ok == false {
		return nil, fmt.Errorf("Matched serviceable is not a P2P serviceable")
	}

	if err := pick.Ready(); err != nil {
		return nil, counter.ErrorWrapper(pick, start, time.Time{}, fmt.Errorf("P2P protocol serviceable is not ready with: %s", err))
	}

	coldStartDone, resp, err := pick.Handle(cmd)
	return resp, counter.ErrorWrapper(pick, start, coldStartDone, err)
}
