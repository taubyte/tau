package patrick

import (
	"errors"
	"fmt"

	"github.com/taubyte/p2p/streams/command"

	kvdbIface "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/kvdb"
)

func (client *Client) DatabaseStats() (kvdbIface.Stats, error) {
	response, err := client.Send("stats", command.Body{"action": "db"})
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
