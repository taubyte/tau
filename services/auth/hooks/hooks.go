package hooks

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/utils/network"
)

// Data is the hook's wire shape. As with repositories, it is no longer the
// constructor's argument: a hook naming its repository by the wrong grammar is
// a defect the compiler should catch, and a map hid it.
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

// GithubHook is a webhook GitHub calls. Provider stays "github" because it
// names the PAYLOAD FORMAT this hook is parsed as — a protocol, not an
// identity — while Repository is the canonical clone-URL key of the repository
// the hook fires for.
type GithubHook struct {
	HookCommon
	GithubId int
	Secret   string

	// Repository is the canonical key. It used to be an int, and it was stored
	// as a fixed-width uint64 VALUE as well as appearing in the reverse index
	// path, so re-keying it changes an encoding at rest and not only a path.
	Repository string
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

// reverseIndex is the row that lets a repository find its own hooks. Losing it
// is SILENT — repo.Hooks() returns an empty slice, which reads exactly like
// "no hooks" — and the cost of that silence is an unregister that leaves the
// provider-side webhook and deploy key in place.
func (h *GithubHook) reverseIndex() string {
	return "/repositories/" + h.Repository + "/hooks/" + h.Id
}

func (h *GithubHook) Delete(ctx context.Context) error {
	err := h.HookCommon.Delete(ctx)
	if err != nil {
		return err
	}

	batch, err := h.KV.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	root := "/hooks/" + h.Id

	err = batch.Delete(root + "/github/id")
	if err != nil {
		return fmt.Errorf("failed to batch delete hook ID: %w", err)
	}

	err = batch.Delete(root + "/github/secret")
	if err != nil {
		return fmt.Errorf("failed to batch delete hook secret: %w", err)
	}

	err = batch.Delete(root + "/github/repository")
	if err != nil {
		return fmt.Errorf("failed to batch delete hook repository: %w", err)
	}

	err = batch.Delete(h.reverseIndex())
	if err != nil {
		return fmt.Errorf("failed to batch delete repository hook reference: %w", err)
	}

	err = batch.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit hook deletion batch: %w", err)
	}

	return nil
}

func (h *GithubHook) Register(ctx context.Context) error {
	err := h.HookCommon.Register(ctx)
	if err != nil {
		return err
	}

	batch, err := h.KV.Batch(ctx)
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to create batch: %w", err)
	}

	root := "/hooks/" + h.Id

	err = batch.Put(h.reverseIndex(), nil)
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to batch repository hook reference: %w", err)
	}

	// The GitHub hook id stays a number because it IS one: it is the id GitHub
	// gave the webhook, which is the provider's to name, not ours.
	err = batch.Put(root+"/github/id", network.UInt64ToBytes(uint64(h.GithubId)))
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to batch hook ID: %w", err)
	}

	err = batch.Put(root+"/github/secret", []byte(h.Secret))
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to batch hook secret: %w", err)
	}

	err = batch.Put(root+"/github/repository", []byte(h.Repository))
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to batch hook repository: %w", err)
	}

	err = batch.Commit()
	if err != nil {
		h.Delete(ctx)
		return fmt.Errorf("failed to commit hook registration batch: %w", err)
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

	switch provider {
	case "github":
		_id, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/id")
		if err != nil {
			return nil, err
		}
		githubID, err := network.BytesToUInt64(_id)
		if err != nil {
			return nil, errors.New("GitHub hook id for Hook `" + hook_id + "` is not an `int`")
		}

		secret, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/secret")
		if err != nil {
			return nil, err
		}

		repository, err := kv.Get(ctx, "/hooks/"+hook_id+"/github/repository")
		if err != nil {
			return nil, err
		}

		key, err := repokey.Parse("https://" + string(repository))
		if err != nil {
			return nil, fmt.Errorf("hook `%s` names repository %q, which is not a key: %w", hook_id, repository, err)
		}

		return NewGithub(kv, hook_id, int(githubID), string(secret), key), nil
	default:
		return nil, errors.New("unknown/unsupported git provider " + provider)
	}
}

// NewGithub builds a GitHub webhook for the repository named by key. It takes a
// repokey.Key for the same reason repositories.New does: the only way to hold
// one is to have parsed a clone URL through the single grammar.
func NewGithub(kv kvdb.KVDB, id string, githubID int, secret string, repository repokey.Key) Hook {
	return &GithubHook{
		HookCommon: HookCommon{
			KV:       kv,
			Id:       id,
			Provider: "github",
		},
		GithubId:   githubID,
		Secret:     secret,
		Repository: repository.String(),
	}
}
