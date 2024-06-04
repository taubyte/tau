package seer

import (
	"fmt"

	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	iface "github.com/taubyte/tau/core/services/seer"
)

func (u *Usage) Announce(services iface.Services, nodeId, clientNodeId string, signature []byte) (response.Response, error) {
	resp, err := u.client.Send("announce", command.Body{"services": services, "id": nodeId, "client": clientNodeId, "signature": signature})
	if err != nil {
		logger.Error("announce failed with:", err.Error())
		return nil, fmt.Errorf("calling announce send failed with: %s", err)
	}

	return resp, nil
}
