package tns

import (
	"fmt"

	"github.com/taubyte/p2p/streams/command"
	kvdbIface "github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/kvdb"
)

func (c *Client) Stats() iface.Stats {
	return (*Stats)(c)
}

func (h *Stats) Database() (kvdbIface.Stats, error) {
	response, err := h.client.Send("stats", command.Body{"action": "db"})
	if err != nil {
		return nil, err
	}

	idata, err := response.Get("stats")
	if err != nil {
		return nil, err
	}

	data, ok := idata.([]byte)
	if !ok {
		return nil, fmt.Errorf("incorrect stats type")
	}

	s := kvdb.NewStats()
	err = s.Decode(data)
	if err != nil {
		return nil, err
	}

	return s, nil
}
