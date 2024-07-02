package seer

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

func (u *Usage) Announce(services iface.Services, nodeId, clientNodeId string, signature []byte) (response.Response, error) {
	resp, err := u.client.Send("announce", command.Body{"services": services, "id": nodeId, "client": clientNodeId, "signature": signature}, u.peers...)
	if err != nil {
		return nil, fmt.Errorf("calling announce send failed with: %s", err)
	}

	return resp, nil
}
