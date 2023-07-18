package p2p

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/auth"
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
	logger.Debug(fmt.Sprintf("Getting hook `%s`", hook_id))
	defer logger.Debug(fmt.Sprintf("Getting hook `%s` done", hook_id))

	response, err := h.client.Send("hooks", streams.Body{"action": "get", "id": hook_id})
	if err != nil {
		logger.Error(err.Error())
		return nil, err
	}

	return h.New(response)
}

func (h *Hooks) List() ([]string, error) {
	response, err := h.client.Send("hooks", streams.Body{"action": "list"})
	if err != nil {
		return nil, err
	}
	ids, err := maps.StringArray(response, "hooks")
	if err != nil {
		return nil, fmt.Errorf("Failed map string array on list error: %v", err)
	}
	return ids, nil
}
