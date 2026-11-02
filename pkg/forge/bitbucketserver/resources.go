package bitbucketserver

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
	Name        string `json:"name"`
	SCMID       string `json:"scmId"`
	Description string `json:"description,omitempty"`
	Public      bool   `json:"public"`
}

// Create creates the repository in the project its key names. Bitbucket
// derives the slug from the name, so the created repository's key is checked
// against the one asked for rather than assumed.
func (c *client) Create(ctx context.Context, key repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return forge.Repository{}, err
	}
	var r repoJSON
	body := createRepoRequest{Name: at.slug, SCMID: "git", Description: opts.Description, Public: !opts.Private}
	if err := c.send(ctx, routeCreateRepository.at(at.project), body, &r); err != nil {
		return forge.Repository{}, c.classify(err, repokey.Key{})
	}
	repo, err := c.repository(r, fmt.Sprintf("repository %q", key))
	if err != nil {
		return forge.Repository{}, err
	}
	if repo.Key != key {
		return forge.Repository{}, fmt.Errorf("forge/bitbucketserver: asked for %q and the forge created %q", key, repo.Key)
	}
	if err := c.defaultBranch(ctx, r, &repo); err != nil {
		return forge.Repository{}, err
	}
	return repo, nil
}

func (c *client) Delete(ctx context.Context, key repokey.Key) error {
	at, err := c.d.coords(key)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeDeleteRepository.at(at.project, at.slug), nil, nil); err != nil {
		return c.classify(err, key)
	}
	return nil
}

// accessKeyRequest is an access key: the public half, a label, and the
// permission, which this driver only ever sends as REPO_READ.
type accessKeyRequest struct {
	Key        newKeyJSON `json:"key"`
	Permission string     `json:"permission"`
}

type newKeyJSON struct {
	Text  string `json:"text"`
	Label string `json:"label"`
}

type accessKeyJSON struct {
	Key        keyJSON `json:"key"`
	Permission string  `json:"permission"`
}

type keyJSON struct {
	ID    int64  `json:"id"`
	Text  string `json:"text"`
	Label string `json:"label"`
}

func (c *client) InstallDeployKey(ctx context.Context, key repokey.Key, title, publicKey string) (forge.Handle, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return "", err
	}
	var body accessKeyRequest
	body.Key.Text, body.Key.Label, body.Permission = publicKey, title, permRead
	var out accessKeyJSON
	if err := c.send(ctx, routeInstallDeployKey.at(at.project, at.slug), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	h, err := handle(out.Key.ID)
	if err != nil {
		return "", err
	}
	if out.Permission != permRead {
		// A key that can do more than read must not stay. The removal is
		// asked for and its own failure is reported beside the refusal.
		removeErr := c.RemoveDeployKey(ctx, key, h)
		return "", errors.Join(fmt.Errorf("forge/bitbucketserver: %s installed deploy key %s on %q with permission %q where %q was asked", c.d.host, h, key, bounded(out.Permission), permRead), removeErr)
	}
	return h, nil
}

func (c *client) RemoveDeployKey(ctx context.Context, key repokey.Key, h forge.Handle) error {
	at, err := c.d.coords(key)
	if err != nil {
		return err
	}
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemoveDeployKey.at(at.project, at.slug, seg), nil, nil); err != nil {
		return c.classify(err, repokey.Key{})
	}
	return nil
}

func (c *client) ListDeployKeys(ctx context.Context, key repokey.Key) ([]forge.InstalledDeployKey, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return nil, err
	}
	var out []forge.InstalledDeployKey
	var bad error
	err = walkPages(ctx, c, func(start int) request {
		return routeListDeployKeys.at(at.project, at.slug).with(pageQuery(start))
	}, func(got []accessKeyJSON) bool {
		for _, k := range got {
			h, err := handle(k.Key.ID)
			if err != nil {
				bad = err
				return false
			}
			out = append(out, forge.InstalledDeployKey{Handle: h, Title: k.Key.Label, PublicKey: withoutComment(k.Key.Text)})
		}
		return true
	})
	if bad != nil {
		return nil, bad
	}
	if err != nil {
		return nil, c.classify(err, key)
	}
	return out, nil
}

// withoutComment is an authorized_keys line's type and key, which is all the
// interface promises.
func withoutComment(text string) string {
	f := strings.Fields(text)
	if len(f) < 2 {
		return text
	}
	return f[0] + " " + f[1]
}

func pageQuery(start int) url.Values {
	return url.Values{"start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(pageLimit)}}
}

type webhookJSON struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

type createHookRequest struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Active        bool              `json:"active"`
	Events        []string          `json:"events"`
	Configuration map[string]string `json:"configuration"`
}

// hookName is what a person reading the repository's settings sees.
const hookName = "tau"

// pushEvent is the one event a hook is registered for.
const pushEvent = "repo:refs_changed"

// RegisterPushHook hands Bitbucket the wire string as the hook's secret, which
// is what makes it sign every delivery with X-Hub-Signature.
func (c *client) RegisterPushHook(ctx context.Context, key repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return "", err
	}
	body := createHookRequest{
		Name:          hookName,
		URL:           callback.String(),
		Active:        true,
		Events:        []string{pushEvent},
		Configuration: map[string]string{"secret": wire},
	}
	var out webhookJSON
	if err := c.send(ctx, routeRegisterPushHook.at(at.project, at.slug), body, &out); err != nil {
		return "", c.classify(err, key)
	}
	return handle(out.ID)
}

func (c *client) ListPushHooks(ctx context.Context, key repokey.Key) ([]forge.InstalledPushHook, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return nil, err
	}
	var out []forge.InstalledPushHook
	var bad error
	err = walkPages(ctx, c, func(start int) request {
		return routeListPushHooks.at(at.project, at.slug).with(pageQuery(start))
	}, func(got []webhookJSON) bool {
		for _, h := range got {
			hd, err := handle(h.ID)
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
	if err != nil {
		return nil, c.classify(err, key)
	}
	return out, nil
}

func (c *client) RemovePushHook(ctx context.Context, key repokey.Key, h forge.Handle) error {
	at, err := c.d.coords(key)
	if err != nil {
		return err
	}
	seg, err := handleSegment(h)
	if err != nil {
		return err
	}
	if err := c.send(ctx, routeRemovePushHook.at(at.project, at.slug, seg), nil, nil); err != nil {
		return c.classify(err, repokey.Key{})
	}
	return nil
}

// handle is a numbered resource's handle. A zero id is a resource nothing
// could ever remove.
func handle(id int64) (forge.Handle, error) {
	if id == 0 {
		return "", errors.New("forge/bitbucketserver: the forge returned no id for the resource it created, so nothing could ever remove it")
	}
	return forge.Handle(strconv.FormatInt(id, 10)), nil
}

// handleSegment is a handle as the one path segment its route has. Every
// handle this driver hands out is a number, so anything else is refused.
func handleSegment(h forge.Handle) (string, error) {
	if _, err := strconv.ParseUint(string(h), 10, 63); err != nil {
		return "", fmt.Errorf("forge/bitbucketserver: the handle %q is not one this driver handed out", bounded(string(h)))
	}
	return string(h), nil
}

// Over HTTPS an HTTP access token is the password. A personal token wants its
// owner's name, which this layer does not know, so the name is the placeholder
// Atlassian's own tooling uses.
func (c *client) CloneAddress(key repokey.Key, over forge.Protocol) (forge.Address, error) {
	at, err := c.d.coords(key)
	if err != nil {
		return forge.Address{}, err
	}
	escaped, err := escapedSegments([]string{at.project, at.slug})
	if err != nil {
		return forge.Address{}, err
	}
	if over == forge.SSH {
		return forge.Address{URL: "ssh://git@" + c.d.sshHost + "/" + escaped + ".git", SSHAccount: "git"}, nil
	}
	return forge.Address{URL: "https://" + c.d.host + "/" + scmSegment + "/" + escaped + ".git", HTTPUsername: "x-token-auth"}, nil
}
