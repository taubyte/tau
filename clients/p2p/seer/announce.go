package p2p

import (
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/seer"
)

func (u *Usage) Announce(services iface.Services, nodeId, clientNodeId string, signature []byte) (streams.Response, error) {
	resp, err := u.client.Send("announce", streams.Body{"services": services, "id": nodeId, "client": clientNodeId, "signature": signature})
	if err != nil {
		logger.Std().Error(fmt.Sprintf("announce failed with: %s", err))
		return nil, fmt.Errorf("calling announce send failed with: %s", err)
	}

	return resp, nil
}
