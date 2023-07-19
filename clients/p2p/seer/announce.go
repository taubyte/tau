package p2p

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
)

func (u *Usage) Announce(services iface.Services, nodeId, clientNodeId string, signature []byte) (response.Response, error) {
	resp, err := u.client.Send("announce", command.Body{"services": services, "id": nodeId, "client": clientNodeId, "signature": signature})
	if err != nil {
		logger.Std().Error(fmt.Sprintf("announce failed with: %s", err))
		return nil, fmt.Errorf("calling announce send failed with: %s", err)
	}

	return resp, nil
}
