package seer

import (
	"encoding/json"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (u *Usage) Heartbeat(usage *iface.UsageData, hostname, nodeId, clientNodeId string, signature []byte) (response.Response, error) {
	usageData, err := cbor.Marshal(usage)
	if err != nil {
		return nil, err
	}

	resp, err := u.client.Send("heartbeat", command.Body{"usage": usageData, "hostname": hostname, "id": nodeId, "client": clientNodeId, "signature": signature}, u.peers...)
	if err != nil {
		return nil, fmt.Errorf("calling heartbeat send failed with: %w", err)
	}
	return resp, nil
}

func (u *Usage) List() ([]string, error) {
	resp, err := u.client.Send("heartbeat", command.Body{"action": "list"}, u.peers...)
	if err != nil {
		logger.Error("Listing ids failed with:", err.Error())
		return nil, fmt.Errorf("calling list send failed with: %w", err)
	}

	_ids, ok := resp["ids"]
	if !ok || _ids == nil {
		return []string{}, nil
	}

	ids, err := maps.StringArray(resp, "ids")
	if err != nil {
		return nil, fmt.Errorf("converting ids to map string array failed with: %s", err)
	}

	return ids, nil
}

func (u *Usage) Get(id string) (*iface.UsageReturn, error) {
	resp, err := u.client.Send("heartbeat", command.Body{"action": "info", "id": id}, u.peers...)
	if err != nil {
		logger.Error(fmt.Sprintf("Getting usage %s failed with: %s", id, err.Error()))
		return &iface.UsageReturn{}, fmt.Errorf("calling info send failed with: %s", err)
	}

	data, ok := resp["usage"]
	if !ok {
		return nil, fmt.Errorf("getting usage for `%s` from body failed", id)
	}

	usageBytes, ok := data.([]byte)
	if !ok {
		return nil, fmt.Errorf("failed getting usage for `%s` from body, got type(%T) expected(%T)", id, data, []byte{})
	}

	usage := &iface.UsageReturn{}
	if err = json.Unmarshal(usageBytes, usage); err != nil {
		return nil, fmt.Errorf("unmarshalling usage failed with: %s", err)
	}

	return usage, nil
}

func (u *Usage) ListServiceId(name string) ([]string, error) {
	resp, err := u.client.Send("heartbeat", command.Body{"action": "listService", "name": name})
	if err != nil {
		logger.Error(fmt.Sprintf("List Specific for %s failed with: %s", name, err.Error()))
		return nil, fmt.Errorf("calling heartbeat listService send failed with: %w", err)
	}

	ret, err := maps.StringArray(resp, "ids")
	if err != nil {
		return nil, fmt.Errorf("calling heartbeat listService failed with: %w", err)
	}

	return ret, nil
}
