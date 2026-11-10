package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v71/github"
	cu "github.com/taubyte/tau/services/auth/crypto"
	"golang.org/x/oauth2"
)

// GitHubClient defines the interface for GitHub operations
type GitHubClient interface {
	Cur() *github.Repository
	Me() *github.User
	GetByID(id string) error
	GetCurrentRepository() (*github.Repository, error)
	CreateRepository(name *string, description *string, private *bool) error
	CreateDeployKey(name *string, key *string) error
	RemoveOtherDeployKeys(name string, keep string) error
	CreatePushHook(name *string, url *string, devMode bool) (int64, string, error)
	ListMyRepos() map[string]RepositoryBasicInfo
	ShortRepositoryInfo(id string) RepositoryShortInfo
}

type githubClient struct {
	*github.Client
	Token              string
	ctx                context.Context
	user               *github.User
	current_repository *github.Repository
}

type RepositoryListOptions github.RepositoryListOptions

// RepositoryShortInfo represents basic repository information
type RepositoryShortInfo struct {
	Name     string `json:"name"`
	FullName string `json:"fullname"`
	URL      string `json:"url"`
	ID       string `json:"id"`
	Error    string `json:"error,omitempty"`
}

// RepositoryBasicInfo represents basic repository information for listing
type RepositoryBasicInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func NewGitHubClient(ctx context.Context, token string) (GitHubClient, error) {
	if token == "" {
		return &githubClient{Client: github.NewClient(http.DefaultClient), ctx: ctx}, nil
	}

	client := github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))

	user, err := verifiedUser(ctx, client, token)
	if err != nil {
		return nil, err
	}

	return &githubClient{client, token, ctx, user, nil}, nil
}

func (client *githubClient) Cur() *github.Repository {
	return client.current_repository
}

func (client *githubClient) Me() *github.User {
	return client.user
}

func (client *githubClient) GetByID(id string) error {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	client.current_repository, _, err = client.Repositories.GetByID(client.ctx, _id)
	return err
}

func (client *githubClient) GetCurrentRepository() (*github.Repository, error) {
	if client.current_repository == nil {
		return nil, errors.New("Client has no current repository")
	}

	return client.current_repository, nil
}

func (client *githubClient) CreateRepository(name *string, description *string, private *bool) (err error) {
	client.current_repository, _, err = client.Repositories.Create(client.ctx, "", &github.Repository{
		Name:        name,
		Private:     private,
		Description: description,
	})

	return
}

func (client *githubClient) CreateDeployKey(name *string, key *string) error {
	if client.current_repository == nil {
		return errors.New("no repository selected")
	}

	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return err
	}

	_, _, err = client.Repositories.CreateKey(client.ctx, owner, repo, &github.Key{
		Title: name,
		Key:   key,
	})

	return err
}

// deployKeyGrace spares a key this young when older ones are removed: it may
// belong to another registration of the repository that is about to store it.
// It goes at the next registration.
const deployKeyGrace = 10 * time.Minute

// RemoveOtherDeployKeys deletes the selected repository's deploy keys titled
// name, except keep (an authorized_keys line). Every registration adds a key and
// none was ever removed, so each Dream restart or re-import left one more live
// credential on the repository.
func (client *githubClient) RemoveOtherDeployKeys(name string, keep string) error {
	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return err
	}
	keepKey := sameDeployKey(keep)

	var errs []error
	opts := &github.ListOptions{PerPage: 100}
	for {
		keys, resp, err := client.Repositories.ListKeys(client.ctx, owner, repo, opts)
		if err != nil {
			return fmt.Errorf("listing deploy keys of %s/%s failed with: %w", owner, repo, err)
		}
		for _, k := range keys {
			if k.GetTitle() != name || sameDeployKey(k.GetKey()) == keepKey || k.CreatedAt == nil || time.Since(k.CreatedAt.Time) < deployKeyGrace {
				continue
			}
			if _, err := client.Repositories.DeleteKey(client.ctx, owner, repo, k.GetID()); err != nil {
				errs = append(errs, fmt.Errorf("deleting deploy key %d of %s/%s failed with: %w", k.GetID(), owner, repo, err))
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return errors.Join(errs...)
		}
		opts.Page = resp.NextPage
	}
}

// sameDeployKey reduces an authorized_keys line to its type and key, the part
// GitHub gives back; the comment is dropped.
func sameDeployKey(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return strings.TrimSpace(line)
	}
	return fields[0] + " " + fields[1]
}

// currentOwnerRepo is the owner and name of the selected repository. The owner
// is the repository's, not the caller's: they differ whenever the repository
// belongs to an organization the caller is a member of.
func (client *githubClient) currentOwnerRepo() (string, string, error) {
	if client.current_repository == nil {
		return "", "", errors.New("no repository selected")
	}
	owner := client.current_repository.GetOwner().GetLogin()
	if owner == "" {
		return "", "", errors.New("repository owner unavailable")
	}
	name := client.current_repository.GetName()
	if name == "" {
		return "", "", errors.New("repository name unavailable")
	}
	return owner, name, nil
}

func (client *githubClient) CreatePushHook(name *string, url *string, devMode bool) (int64, string, error) {
	if client.current_repository == nil {
		return 0, "", errors.New("no repository selected")
	}

	secret, err := cu.GenerateSecretString()
	if err != nil {
		return 0, "", err
	}

	// Don't create hooks in devMode as we are faking pushes
	if devMode {
		return 1, secret, nil
	}

	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return 0, "", err
	}

	hk, _, err := client.Repositories.CreateHook(client.ctx, owner, repo, &github.Hook{
		Events: []string{
			"push",
		},
		Config: &github.HookConfig{
			ContentType: github.Ptr("json"),
			Secret:      github.Ptr(secret),
			InsecureSSL: github.Ptr("0"),
			URL:         url,
		},
		Active: github.Ptr(true),
	})

	if err != nil {
		return 0, "", err
	}

	return *(hk.ID), secret, err
}

func (client *githubClient) ListMyRepos() map[string]RepositoryBasicInfo {
	repos := make(map[string]RepositoryBasicInfo)
	for i := 1; ; i++ {
		rlo := github.RepositoryListByAuthenticatedUserOptions{ListOptions: github.ListOptions{Page: i, PerPage: 100}, Sort: "created"}

		_repos, _, err := client.Repositories.ListByAuthenticatedUser(client.ctx, &rlo)
		if err == nil && len(_repos) > 0 {
			for _, v := range _repos {
				repos[fmt.Sprintf("%d", *(v.ID))] = RepositoryBasicInfo{
					Name: *(v.FullName),
					URL:  *(v.URL),
				}
			}
		} else {
			break
		}
	}
	return repos
}

func (client *githubClient) ShortRepositoryInfo(id string) RepositoryShortInfo {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return RepositoryShortInfo{
			Error: "Incorrect repository ID",
		}
	}

	_repoInfo, _, err := client.Repositories.GetByID(client.ctx, _id)
	if err != nil {
		return RepositoryShortInfo{
			Error: fmt.Sprintf("Error %s", err),
		}
	}

	return RepositoryShortInfo{
		Name:     *(_repoInfo.Name),
		FullName: *(_repoInfo.FullName),
		URL:      *(_repoInfo.URL),
		ID:       id,
	}
}

// verifiedTokenTTL is how long a token GitHub accepted is trusted without
// asking again, and so how long a revoked token keeps working here. Every
// authenticated HTTP request builds a client, and asking GitHub who the token
// belongs to took ~0.5s, so a CLI listing n jobs waited n times that and spent
// n calls of the token's hourly rate limit. A burst like that fits in a minute.
var (
	verifiedTokenTTL = time.Minute
	verifiedTokenMax = 4096
)

type verifiedToken struct {
	user    *github.User
	expires time.Time
}

// verifiedTokens is keyed by the token's hash, so tokens are not kept in memory.
var (
	verifiedTokens     = make(map[[sha256.Size]byte]verifiedToken)
	verifiedTokensLock sync.Mutex
)

func verifiedUser(ctx context.Context, client *github.Client, token string) (*github.User, error) {
	key := sha256.Sum256([]byte(token))

	verifiedTokensLock.Lock()
	vt, ok := verifiedTokens[key]
	verifiedTokensLock.Unlock()
	if ok && time.Now().Before(vt.expires) {
		return vt.user, nil
	}

	user, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	verifiedTokensLock.Lock()
	defer verifiedTokensLock.Unlock()
	if len(verifiedTokens) >= verifiedTokenMax {
		for k, v := range verifiedTokens {
			if !now.Before(v.expires) {
				delete(verifiedTokens, k)
			}
		}
		if len(verifiedTokens) >= verifiedTokenMax {
			clear(verifiedTokens)
		}
	}
	verifiedTokens[key] = verifiedToken{user: user, expires: now.Add(verifiedTokenTTL)}
	return user, nil
}
