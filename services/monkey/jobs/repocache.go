package jobs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/git"
)

const (
	repoCacheMaxAge     = 7 * 24 * time.Hour
	repoCachePruneEvery = time.Hour
)

var (
	// gitCacheRoot holds one clone per <projectID>/<repoID>, reused across jobs.
	gitCacheRoot = filepath.Join(os.TempDir(), "tau-git")
	cacheIDRe    = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

	repoLocksMu sync.Mutex
	repoLocks   = map[string]chan struct{}{}
	lastPrune   time.Time
)

func repoLock(dir string) chan struct{} {
	repoLocksMu.Lock()
	defer repoLocksMu.Unlock()
	l, ok := repoLocks[dir]
	if !ok {
		l = make(chan struct{}, 1)
		repoLocks[dir] = l
	}
	return l
}

// lockRepoDir returns the clone dir of a project's repo, held exclusively
// until unlock: two jobs of one project never share it at the same time.
func lockRepoDir(ctx context.Context, projectID, repoID string) (dir string, unlock func(), err error) {
	dir = filepath.Join(gitCacheRoot, projectID, repoID)
	l := repoLock(dir)
	select {
	case l <- struct{}{}:
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		<-l
		return "", nil, err
	}
	funlock, err := fileLock(ctx, dir+".lock")
	if err != nil {
		<-l
		return "", nil, err
	}
	pruneRepoCache()
	return dir, func() { funlock(); <-l }, nil
}

// pruneRepoCache removes clone dirs unused for a week, at most hourly, and
// never one a job holds. A used dir has its mtime refreshed by touchRepoDir.
func pruneRepoCache() {
	repoLocksMu.Lock()
	if time.Since(lastPrune) < repoCachePruneEvery {
		repoLocksMu.Unlock()
		return
	}
	lastPrune = time.Now()
	repoLocksMu.Unlock()

	dirs, _ := filepath.Glob(filepath.Join(gitCacheRoot, "*", "*"))
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() || time.Since(fi.ModTime()) < repoCacheMaxAge {
			continue
		}
		l := repoLock(d)
		select {
		case l <- struct{}{}:
			if funlock, err := fileLock(nil, d+".lock"); err == nil {
				os.RemoveAll(d) // the .lock file stays: removing it races another process's open
				funlock()
			}
			<-l
		default:
		}
	}
}

func touchRepoDir(dir string) {
	now := time.Now()
	os.Chtimes(dir, now, now)
}

// openRepo clones url into the project's reusable dir (held until the job
// ends), or into a throwaway temp dir when the ids are not safe path parts.
// commit is the one the tree ends on; empty means the branch tip.
func (c *Context) openRepo(repoID, url, key, commit string, opts ...git.Option) (*git.Repository, error) {
	opts = append([]git.Option{
		git.URL(url), git.SSHKey(key), git.Shallow(),
		git.Branch(c.Job.Meta.Repository.Branch), git.Output(c.LogFile),
	}, opts...)
	if !cacheIDRe.MatchString(c.ProjectID) || !cacheIDRe.MatchString(repoID) {
		return git.New(c.ctx, append(opts, git.Temporary())...)
	}
	dir, unlock, err := lockRepoDir(c.ctx, c.ProjectID, repoID)
	if err != nil {
		return nil, err
	}
	c.unlocks = append(c.unlocks, unlock)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	touchRepoDir(dir)
	return git.New(c.ctx, append(opts, git.Root(dir), git.Reuse(commit))...)
}

func (c *Context) releaseRepos() {
	for _, u := range c.unlocks {
		u()
	}
	c.unlocks = nil
}
