package auth

import (
	"crypto/sha256"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-github/v71/github"
)

// A caller's repositories are listed from GitHub, all of them, for every
// project listing, and tau asks for that listing several times in a row (import,
// select, push). With hundreds of repositories each listing is seconds and
// several GitHub calls, so it is kept briefly per token. Anything a caller
// changes through Auth drops that caller's copy, so its new or removed projects
// show at once; repositories changed elsewhere show within repoListTTL.
var (
	repoListTTL = time.Minute
	repoListMax = 1024
)

type repoList struct {
	newestFirst []string
	repos       map[string]RepositoryBasicInfo
	expires     time.Time
}

var (
	repoLists     = make(map[[sha256.Size]byte]repoList)
	repoListsLock sync.Mutex
)

func repoListKey(token string) [sha256.Size]byte { return sha256.Sum256([]byte("repos\x00" + token)) }

func cachedRepoList(token string) (repoList, bool) {
	if token == "" {
		return repoList{}, false
	}
	repoListsLock.Lock()
	defer repoListsLock.Unlock()
	l, ok := repoLists[repoListKey(token)]
	if !ok || !time.Now().Before(l.expires) {
		return repoList{}, false
	}
	return l, true
}

func storeRepoList(token string, newestFirst []string, repos map[string]RepositoryBasicInfo) {
	if token == "" {
		return
	}
	repoListsLock.Lock()
	defer repoListsLock.Unlock()
	if len(repoLists) >= repoListMax {
		clear(repoLists)
	}
	repoLists[repoListKey(token)] = repoList{newestFirst: newestFirst, repos: maps.Clone(repos), expires: time.Now().Add(repoListTTL)}
}

// forgetRepoList drops the caller's copy after it changed something.
func forgetRepoList(client GitHubClient) {
	gc, ok := client.(*githubClient)
	if !ok || gc.Token == "" {
		return
	}
	repoListsLock.Lock()
	defer repoListsLock.Unlock()
	delete(repoLists, repoListKey(gc.Token))
}

// A repository read by id (and the caller's permissions on it) is kept per
// token for repoTTL: GET /projects/{id} read each of a project's two
// repositories twice, so a console polling it spent four calls of the
// caller's hourly GitHub limit each time. A removed collaborator keeps
// access for at most repoTTL. Failures are not kept.
var (
	repoTTL = time.Minute
	repoMax = 4096
)

type cachedRepoEntry struct {
	repo    *github.Repository
	expires time.Time
}

var (
	repos     = make(map[[sha256.Size]byte]cachedRepoEntry)
	reposLock sync.Mutex
)

func repoKey(token string, id int64) [sha256.Size]byte {
	return sha256.Sum256([]byte("repo\x00" + token + "\x00" + strconv.FormatInt(id, 10)))
}

// repoByID is Repositories.GetByID through the cache. Callers must not modify
// the repository returned.
func (client *githubClient) repoByID(id int64) (*github.Repository, error) {
	if client.Token == "" {
		repo, _, err := client.Repositories.GetByID(client.ctx, id)
		return repo, err
	}
	key := repoKey(client.Token, id)
	reposLock.Lock()
	e, ok := repos[key]
	reposLock.Unlock()
	if ok && time.Now().Before(e.expires) {
		return e.repo, nil
	}

	repo, _, err := client.Repositories.GetByID(client.ctx, id)
	if err != nil {
		return nil, err
	}
	reposLock.Lock()
	defer reposLock.Unlock()
	if len(repos) >= repoMax {
		clear(repos)
	}
	repos[key] = cachedRepoEntry{repo: repo, expires: time.Now().Add(repoTTL)}
	return repo, nil
}
