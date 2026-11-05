package dream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/pkg/git/repokey"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/uploads"
)

// Push is what a universe builds: a commit of a repository its mirror holds
// (Commit), as a forge's delivery would name it.
type Push struct {
	Repository string
	Ref        string
	Commit     string
}

// Outcome is a push once it has ended: its plan, then the job of each environment
// the plan queued, in the plan's order.
type Outcome struct {
	Plan         *buildv2.Job
	Environments []*buildv2.Job
}

// Mirror is the directory this universe's runners clone every repository from,
// file:// in their configuration: a repository key's folder under it.
func (u *Universe) Mirror() string { return u.mirror }

// Commit makes dir the newest commit of repository's branch in the mirror, and is
// that commit: what a push of the folder would carry. dir itself is only read.
func (u *Universe) Commit(repository, branch, dir string) (string, error) {
	key, err := repokey.Parse("https://" + repository)
	if err != nil {
		return "", fmt.Errorf("dream: %q is not a repository: %w", repository, err)
	}
	at := filepath.Join(u.mirror, filepath.FromSlash(key.String()))
	repo, err := gogit.PlainOpen(at)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		repo, err = gogit.PlainInitWithOptions(at, &gogit.PlainInitOptions{
			InitOptions: gogit.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName(branch)},
		})
	}
	if err != nil {
		return "", fmt.Errorf("dream: the mirror's %s: %w", key, err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := replaceTree(at, dir); err != nil {
		return "", fmt.Errorf("dream: copying %s into the mirror: %w", dir, err)
	}
	if err := tree.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return "", err
	}
	sha, err := tree.Commit("dream: "+filepath.Base(dir), &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author:            &object.Signature{Name: "dream", Email: "dream@localhost", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return sha.String(), nil
}

// replaceTree makes at's working tree dir's files, its .git kept.
func replaceTree(at, dir string) error {
	files, err := filesOf(dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(at)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(at, e.Name())); err != nil {
				return err
			}
		}
	}
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		to := filepath.Join(at, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(to, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// filesOf is every file under dir, relative to it, outside any .git.
func filesOf(dir string) ([]string, error) {
	var files []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(rel string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".git":
			return fs.SkipDir
		case !d.IsDir():
			files = append(files, filepath.FromSlash(rel))
		}
		return nil
	})
	return files, err
}

// Upload keeps dir in the universe's uploads as a commit of repository's branch,
// with built standing in for what its packages' recipes make: a package's name to
// the file it builds to. It is the push that commit is, what `tau build` hands a
// development cloud; dir itself is only read.
func (u *Universe) Upload(repository, branch, dir string, built map[string]string) (Push, error) {
	recipes, err := u.recipes(repository, branch, dir)
	if err != nil {
		return Push{}, err
	}
	products := map[string]string{}
	named := map[string]bool{}
	for _, byName := range recipes {
		for name, digest := range byName {
			if file, ok := built[name]; ok {
				products[digest], named[name] = file, true
			}
		}
	}
	for name := range built {
		if !named[name] {
			return Push{}, fmt.Errorf("dream: %s builds no package/%s", dir, name)
		}
	}

	files, err := filesOf(dir)
	if err != nil {
		return Push{}, err
	}
	var bundle bytes.Buffer
	if err := artifact.PackBundle(&bundle, dir, files, products); err != nil {
		return Push{}, err
	}
	held, err := uploads.Open(u.uploads)
	if err != nil {
		return Push{}, err
	}
	ref := "refs/heads/" + branch
	commit, err := held.Put(repository, ref, &bundle)
	if err != nil {
		return Push{}, err
	}
	return Push{Repository: repository, Ref: ref, Commit: commit}, nil
}

// recipes is the recipe of each package a push of the tree at dir builds: a
// project's configuration, from its own compile, or a code repository's, against
// what the register publishes for the project it is linked to, as a cloud plans
// either.
func (u *Universe) recipes(repository, branch, dir string) (map[string]map[string]string, error) {
	if _, err := os.Stat(filepath.Join(dir, "project.yaml")); err == nil {
		return uploads.Recipes(dir, branch)
	}
	reg, err := u.register()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	linked, err := reg.Repository(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("dream: %s is no project's: %w", repository, err)
	}
	environments, err := reg.Environments(ctx, linked.Project)
	if err != nil {
		return nil, err
	}
	deployments := make([]artifact.Deployment, 0, len(environments))
	for _, environment := range environments {
		pointer, err := reg.Resolve(ctx, linked.Project, environment)
		if err != nil {
			return nil, err
		}
		compiled, err := reg.Object(ctx, pointer.Object)
		if err != nil {
			return nil, err
		}
		p, err := specs.Load(compiled)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, artifact.Deployment{Environment: environment, Project: p})
	}
	return uploads.CodeRecipes(deployments, repository, dir)
}

// nodes is the universe's nodes, in the order they started.
func (u *Universe) nodes() []*instance.Instance {
	built.Lock()
	defer built.Unlock()
	out := make([]*instance.Instance, 0, len(u.pids))
	for _, pid := range u.pids {
		if r, ok := built.byPeer[pid]; ok {
			out = append(out, r.node)
		}
	}
	return out
}

// register is the universe's register, on the first node running one.
func (u *Universe) register() (*pkgregister.Register, error) {
	for _, n := range u.nodes() {
		if n.Register() != nil && n.Register().Local() != nil {
			return n.Register().Local(), nil
		}
	}
	return nil, errors.New("dream: no node runs the register")
}

// Link names project's repositories in the universe's register, as `tau link`
// would: config is the one whose pushes are deploys.
func (u *Universe) Link(ctx context.Context, project, config string, code ...string) error {
	reg, err := u.register()
	if err != nil {
		return err
	}
	_, err = reg.LinkProject(ctx, pkgregister.Link{Project: project, Config: config, Code: code, Claimant: "dream"})
	return err
}
