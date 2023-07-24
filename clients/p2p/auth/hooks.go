package p2p

import (
	"errors"
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

type Hooks Client

func (c *Client) Hooks() iface.Hooks {
	return (*Hooks)(c)
}

func (h *Hooks) New(obj map[string]interface{}) (iface.Hook, error) {
	id, err := maps.String(obj, "id")
	if err != nil {
		return nil, errors.New("Creating hook: " + err.Error())
	}

	provider, err := maps.String(obj, "provider")
	if err != nil {
		return nil, errors.New("Creating hook: " + err.Error())
	}

	logger.Error(obj)

	switch provider {
	case "github":
		github_id, err := maps.Int(obj, "github_id")
		if err != nil {
			return nil, errors.New("Creating hook: " + err.Error())
		}

		secret, err := maps.String(obj, "secret")
		if err != nil {
			return nil, errors.New("Creating hook: " + err.Error())
		}

		return &iface.GithubHook{
			Id:       id,
			GithubId: github_id,
			Secret:   secret,
		}, nil
	default:
		return nil, err
	}
}

func (h *Hooks) Get(hook_id string) (iface.Hook, error) {
	logger.Debugf("Getting hook `%s`", hook_id)
	defer logger.Debugf("Getting hook `%s` done", hook_id)

	response, err := h.client.Send("hooks", command.Body{"action": "get", "id": hook_id})
	if err != nil {
		logger.Error(err)
		return nil, err
	}

	return h.New(response)
}

func (h *Hooks) List() ([]string, error) {
	response, err := h.client.Send("hooks", command.Body{"action": "list"})
	if err != nil {
		return nil, err
	}
	ids, err := maps.StringArray(response, "hooks")
	if err != nil {
		return nil, fmt.Errorf("failed map string array on list error: %v", err)
	}
	return ids, nil
}
