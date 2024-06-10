package auth

import (
	"errors"
	"fmt"

	kvdbIface "github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/pkg/kvdb"
)

func (c *Client) Stats() iface.Stats {
	return (*Stats)(c)
}

func (h *Stats) Database() (kvdbIface.Stats, error) {
	response, err := h.client.Send("stats", command.Body{"action": "db"}, h.peers...)
	if err != nil {
		return nil, fmt.Errorf("sending stats.db request failed with %w", err)
	}

	idata, err := response.Get("stats")
	if err != nil {
		return nil, fmt.Errorf("getting stats from response failed with %w", err)
	}

	data, ok := idata.([]byte)
	if !ok {
		return nil, errors.New("incorrect stats type")
	}

	s := kvdb.NewStats()
	err = s.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decoding stats failed with %w", err)
	}

	return s, nil
}
