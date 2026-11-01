package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

type createRepoRequest struct {
	SCM         string `json:"scm"`
	Description string `json:"description,omitempty"`
	IsPrivate   bool   `json:"is_private"`
}

// Bitbucket creates under any workspace, the caller's personal one included,
// through one endpoint.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	var r repoJSON
	body := createRepoRequest{SCM: "git", Description: opts.Description, IsPrivate: opts.Private}
	if err := c.send(ctx, routeCreateRepository.at(key.Owner, key.Name), body, &r); err != nil {
		return forge.Repository{}, c.classifyResource(err)
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.Repository{}, err
	}
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/bitbucket: asked for %q and the forge created %q", key, repo.Key)
	}
	return repo, nil
}

func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	if err := c.send(ctx, routeDeleteRepository.at(key.Owner, key.Name), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// createKeyRequest is the whole of a Bitbucket deploy key: the public half and
// a label. There is no read-only field because there is no other kind.
type createKeyRequest struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type deployKeyJSON struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Label string `json:"label"`
}

func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	var out deployKeyJSON
	body := createKeyRequest{Key: publicKey, Label: title}
	if err := c.send(ctx, routeInstallDeployKey.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return handle(out.ID)
}

func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemoveDeployKey.at(key.Owner, key.Name, seg), nil, nil); err != nil {
		return c.classifyResource(err)
	}
	return nil
}

func (c *client) ListDeployKeys(ctx context.Context, key repokey.Key) ([]forge.InstalledDeployKey, error) {
	var out []forge.InstalledDeployKey
	var bad error
	err := walkPages(ctx, c, func(page int) request {
		return routeListDeployKeys.at(key.Owner, key.Name).with(pageQuery(page))
	}, key, func(_ int, got []deployKeyJSON) bool {
		for _, k := range got {
			h, err := handle(k.ID)
			if err != nil {
				bad = err
				return false
			}
			out = append(out, forge.InstalledDeployKey{Handle: h, Title: k.Label, PublicKey: k.Key})
		}
		return true
	})
	if bad != nil {
		return nil, bad
	}
	return out, err
}

type hookJSON struct {
	UUID string `json:"uuid"`
	URL  string `json:"url"`
}

func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	var out []forge.InstalledPushHook
	var bad error
	err := walkPages(ctx, c, func(page int) request {
		return routeListPushHooks.at(key.Owner, key.Name).with(pageQuery(page))
	}, key, func(_ int, got []hookJSON) bool {
		for _, h := range got {
			hd, err := uuidHandle(h.UUID)
			if err != nil {
				bad = err
				return false
			}
			out = append(out, forge.InstalledPushHook{Handle: hd, Callback: h.URL})
		}
		return true
	})
	if bad != nil {
		return nil, bad
	}
	return out, err
}

func pageQuery(page int) url.Values {
	return url.Values{"pagelen": {strconv.Itoa(listPageSize)}, "page": {strconv.Itoa(page)}}
}

type createHookRequest struct {
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Active      bool     `json:"active"`
	Events      []string `json:"events"`
	Secret      string   `json:"secret"`
}

// hookDescription is what a person reading the repository's settings sees.
const hookDescription = "tau"

// RegisterPushHook hands Bitbucket the wire string as the hook's secret, which
// is what makes Bitbucket sign every delivery with X-Hub-Signature.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	var out hookJSON
	body := createHookRequest{
		Description: hookDescription,
		URL:         callback.String(),
		Active:      true,
		Events:      []string{"repo:push"},
		Secret:      wire,
	}
	if err := c.send(ctx, routeRegisterPushHook.at(key.Owner, key.Name), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return uuidHandle(out.UUID)
}

func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemovePushHook.at(key.Owner, key.Name, seg), nil, nil); err != nil {
		return c.classifyResource(err)
	}
	return nil
}

// handle is a numbered resource's handle. A zero id is a resource nothing
// could ever remove.
func handle(id int64) (forge.Handle, error) {
	if id == 0 {
		return "", errors.New("forge/bitbucket: the forge returned no id for the resource it created, so nothing could ever remove it")
	}
	return forge.Handle(strconv.FormatInt(id, 10)), nil
}

// uuidHandle is a webhook's handle: the UUID Bitbucket gave it, braces and all,
// checked when it arrives so a handle that could never be sent back is refused
// now rather than when somebody tries to remove the hook.
func uuidHandle(id string) (forge.Handle, error) {
	if id == "" {
		return "", errors.New("forge/bitbucket: the forge returned no uuid for the hook it created, so nothing could ever remove it")
	}
	if _, err := handleSegment(forge.Handle(id)); err != nil {
		return "", err
	}
	return forge.Handle(id), nil
}

func handleSegment(h forge.Handle) (string, error) {
	if h == "" {
		return "", errors.New("forge/bitbucket: removing a resource needs the handle the forge gave it")
	}
	if strings.Contains(string(h), "/") {
		return "", fmt.Errorf("forge/bitbucket: the handle %q spans more than one path segment, and the route it fills has one; the removal would address a resource under a collection nobody named", bounded(string(h)))
	}
	if err := addressesItself(string(h)); err != nil {
		return "", fmt.Errorf("forge/bitbucket: the handle names no resource: %w", err)
	}
	return string(h), nil
}

// Over HTTPS a Bitbucket access token authenticates as the literal user
// x-token-auth.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	escaped, err := escapedSegments([]string{key.Owner, key.Name})
	if err != nil {
		return forge.Address{}, err
	}
	path := "/" + escaped + ".git"
	if over == forge.SSH {
		return forge.Address{URL: "ssh://git@" + c.d.host + path, SSHAccount: "git"}, nil
	}
	return forge.Address{URL: "https://" + c.d.host + path, HTTPUsername: "x-token-auth"}, nil
}
