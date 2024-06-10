package patrick

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	iface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

func (client *Client) Lock(jid string, eta uint32) error {
	if _, err := client.Send("patrick", command.Body{"action": "lock", "jid": jid, "eta": eta}); err != nil {
		return fmt.Errorf("failed send lock with error: %w", err)
	}

	return nil
}

func (client *Client) IsLocked(jid string) (bool, error) {
	resp, err := client.Send("patrick", command.Body{"action": "isLocked", "jid": jid})
	if err != nil {
		return false, fmt.Errorf("failed send isLocked with error: %w", err)
	}

	locked, err := maps.Bool(resp, "locked")
	if err != nil {
		return false, err
	}

	by, err := maps.String(resp, "locked-by")
	if err != nil {
		return false, err
	}

	return locked && (by == client.node.ID().String()), nil
}

// TODO: delete
func (client *Client) Unlock(jid string) error {
	if _, err := client.Send("patrick", command.Body{"action": "unlock", "jid": jid}); err != nil {
		return fmt.Errorf("failed send unlock with error: %w", err)
	}

	return nil
}

func (client *Client) Done(jid string, cid_log map[string]string, assetCid map[string]string) error {
	if _, err := client.Send("patrick", command.Body{"action": "done", "jid": jid, "cid": cid_log, "assetCid": assetCid}); err != nil {
		return fmt.Errorf("failed sending done with error: %w", err)
	}

	return nil
}

func (client *Client) Failed(jid string, cid_log map[string]string, assetCid map[string]string) error {
	if _, err := client.Send("patrick", command.Body{"action": "failed", "jid": jid, "cid": cid_log, "assetCid": assetCid}); err != nil {
		return fmt.Errorf("failed sending failed with error: %w", err)
	}

	return nil
}

func (client *Client) Cancel(jid string, cid_log map[string]string) (interface{}, error) {
	resp, err := client.Send("patrick", command.Body{"action": "cancel", "jid": jid, "cid": cid_log})
	if err != nil {
		return nil, fmt.Errorf("failed sending cancel with error: %w", err)
	}

	return resp, nil
}

func (client *Client) List() ([]string, error) {
	resp, err := client.Send("patrick", command.Body{"action": "list", "jid": ""})
	if err != nil {
		return nil, fmt.Errorf("failed sending list with error: %w", err)
	}

	ids, err := maps.StringArray(resp, "Ids")
	if err != nil {
		return nil, fmt.Errorf("failed map string array with error: %w", err)
	}

	return ids, nil
}

func (client *Client) Timeout(jid string) error {
	if _, err := client.Send("patrick", command.Body{"action": "timeout", "jid": jid}); err != nil {
		return fmt.Errorf("failed sending timeout with error: %w", err)
	}

	return nil
}

func (client *Client) Get(jid string) (*iface.Job, error) {
	var job iface.Job
	resp, err := client.Send("patrick", command.Body{"action": "info", "jid": jid})
	if err != nil {
		return nil, fmt.Errorf("failed sending list with error: %w", err)
	}

	_job, ok := resp["job"]
	if !ok {
		return nil, fmt.Errorf("could not find job %s", jid)
	}

	job_byte, err := cbor.Marshal(_job)
	if err != nil {
		return nil, fmt.Errorf("failed marshal get job with error: %w", err)
	}

	if err = cbor.Unmarshal(job_byte, &job); err != nil {
		return nil, fmt.Errorf("failed unmarshal get job with error: %w", err)
	}

	return &job, nil
}
