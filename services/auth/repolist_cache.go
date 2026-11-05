package auth

import (
	"crypto/sha256"
	"maps"
	"sync"
	"time"
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
