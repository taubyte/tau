package hooks

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/utils/maps"
	"github.com/taubyte/utils/network"
)

type Data map[string]interface{}

var logger = log.Logger("tau.auth.hooks")

type Hook interface {
	Register(ctx context.Context) error
	Delete(ctx context.Context) error
	Serialize() Data
	ProviderID() string
	ID() string
}

type HookCommon struct {
	KV       kvdb.KVDB
	Id       string
	Provider string
}

func (h *HookCommon) Register(ctx context.Context) error {
	return h.KV.Put(ctx, "/hooks/"+h.Id+"/provider", []byte(h.Provider))
}

func (h *HookCommon) Delete(ctx context.Context) error {
	return h.KV.Delete(ctx, "/hooks/"+h.Id+"/provider")
}

func (h *HookCommon) ID() string {
	return h.Id
}

type GithubHook struct {
	HookCommon
	GithubId   int
	Secret     string
	Repository int
}

func (h *GithubHook) Serialize() Data {
	return Data{
		"id":         h.Id,
		"provider":   h.Provider,
		"github_id":  h.GithubId,
		"secret":     h.Secret,
		"repository": h.Repository,
	}
}

func (h *GithubHook) ProviderID() string {
	return strconv.Itoa(h.GithubId)
}

func (h *GithubHook) Delete(ctx context.Context) error {
	err := h.HookCommon.Delete(ctx)
	if err != nil {
		return err
	}
	// TODO: Make common vars
	root := "/hooks/" + h.Id

	var lerror error
	err = h.KV.Delete(ctx, root+"/github/id")
	if err != nil {
		lerror = err
		logger.Error(err)
	}
	err = h.KV.Delete(ctx, root+"/github/secret")
	if err != nil {
		lerror = err
		logger.Error(err)
	}
	err = h.KV.Delete(ctx, root+"/github/repository")
	if err != nil {
		lerror = err
		logger.Error(err)
	}
	err = h.KV.Delete(ctx, fmt.Sprintf("/repositories/github/%d/hooks/%s", h.Repository, h.Id))
	if err != nil {
		lerror = err
		logger.Error(err)
	}

	return lerror
}

func (h *GithubHook) Register(ctx context.Context) error {
	err := h.HookCommon.Register(ctx)
	if err != nil {
		return err
	}

	root := "/hooks/" + h.Id

	err = h.KV.Put(ctx, fmt.Sprintf("/repositories/github/%d/hooks/%s", h.Repository, h.Id), nil)
	if err != nil {
		h.Delete(ctx)
		return err
	}

	err = h.KV.Put(ctx, root+"/github/id", network.UInt64ToBytes(uint64(h.GithubId)))
	if err != nil {
		h.Delete(ctx)
		return err
	}
	err = h.KV.Put(ctx, root+"/github/secret", []byte(h.Secret))
	if err != nil {
		h.Delete(ctx)
		return err
	}

	err = h.KV.Put(ctx, root+"/github/repository", network.UInt64ToBytes(uint64(h.Repository)))
	if err != nil {
		h.Delete(ctx)
		return err
	}
	return nil
}

func Exist(ctx context.Context, kv kvdb.KVDB, id string) bool {
	ret, err := kv.Get(ctx, "/hooks/"+id+"/provider")
	if err != nil || ret == nil {
		return false
	}
	return true
}

func Fetch(ctx context.Context, kv kvdb.KVDB, hook_id string) (Hook, error) {
	_provider, err := kv.Get(ctx, "/hooks/"+hook_id+"/provider")
	if err != nil {
		return nil, err
	}
	provider := string(_provider)

	data := Data{
		"id":       hook_id,
		"provider": provider,
	}
	switch provider {
	case "github":
		_id, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/id")
		if err != nil {
			return nil, err
		}
		id, err := network.BytesToUInt64(_id)
		if err != nil {
			return nil, errors.New("Repository ID for Hook `" + hook_id + "` is not an `int`")
		}

		data["github_id"] = int(id)

		_secret, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/secret")
		if err != nil {
			return nil, err
		}
		data["secret"] = string(_secret)

		_repository, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/repository")
		if err != nil {
			return nil, err
		}
		repository, err := network.BytesToUInt64(_repository)
		if err != nil {
			return nil, errors.New("Repository ID for Hook `" + hook_id + "` is not an `int`")
		}

		data["repository"] = int(repository)
	default:
		return nil, errors.New("unknown/unsupported git provider " + provider)
	}

	return New(kv, data)
}

func New(kv kvdb.KVDB, data Data) (Hook, error) {
	id, err := maps.String(data, "id")
	if err != nil {
		return nil, err
	}

	provider, err := maps.String(data, "provider")
	if err != nil {
		return nil, err
	}

	switch provider {
	case "github":
		github_id, err := maps.Int(data, "github_id")
		if err != nil {
			return nil, err
		}
		repository, err := maps.Int(data, "repository")
		if err != nil {
			return nil, err
		}
		secret, err := maps.String(data, "secret")
		if err != nil {
			return nil, err
		}

		return &GithubHook{
			HookCommon: HookCommon{
				KV:       kv,
				Id:       id,
				Provider: provider,
			},
			GithubId:   github_id,
			Secret:     secret,
			Repository: repository,
		}, nil
	default:
		return nil, fmt.Errorf("unknown hook type `%s` ", provider)
	}
}
