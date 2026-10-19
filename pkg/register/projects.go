package register

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// projects.go is the register's second table: which project id belongs to
// which repositories, written when a developer links a project and before
// anything is ever published for it.
//
// THE PROJECT ID IS THE AUTHOR'S. It is the `id` in project.yaml, minted by
// tcc's generator on the developer's machine; nothing here generates one. What
// this table decides is who may publish under it: the configuration repository
// that claimed the id first.
//
// IT SHARES A STORE WITH THE PUBLICATIONS AND NOTHING ELSE. Every key below
// sits under recordsPrefix ("r/"), and no publication key does — publications
// own o/, p/, h/, c/, m/ and d/. Publish never writes under r/ and nothing in
// this file writes outside it, so neither table can overwrite or delete a row
// of the other. The one link between them is a READ: a deploy checks that the
// compiled project's id is claimed by the repository it was pushed to
// (services/build/deploy) before it publishes.
//
// # The key layout, and why each discriminator is in the path
//
//	r/claim/<project>/<config>          {at, claimant}  one per (project, config repository)
//	r/code/<project>/<config>/<code>    {at}            one per code repository a claim attaches
//	r/repo/<repository>/<project>       {at, config}    one per (repository, project) that claims it
//	r/mine/<claimant>/<project>         {at, config}    one per (claimant, project): "my projects"
//
// <config>, <code>, <repository> and <claimant> are base64url of the
// normalised value, because a repository key is `host/owner/name` and a
// separator inside a segment would let two keys spell one row.
//
// NO ROW IS EVER CONTENDED. The store is a CRDT with last-write-wins per key
// and no compare-and-set, so two nodes linking the same id from different
// repositories at the same moment both succeed locally. They write DIFFERENT
// keys — the claiming repository is in the path — and both claims survive the
// merge. Which one owns the id is then decided on READ, the same way on every
// node: the earliest `at`, then the smaller repository key. A claim that can
// already see an earlier one is refused when it is made; one that could not is
// simply never the owner on read. The same rule decides a repository two
// projects attach.

const (
	recordsPrefix = "r" + sep
	claimPrefix   = recordsPrefix + "claim" + sep
	codePrefix    = recordsPrefix + "code" + sep
	repoPrefix    = recordsPrefix + "repo" + sep
	minePrefix    = recordsPrefix + "mine" + sep
)

var (
	// ErrProjectClaimed is a project id another configuration repository owns.
	// The fix is the author's: project.yaml's id is theirs to regenerate.
	ErrProjectClaimed = errors.New("register: project id is already claimed")

	// ErrRepositoryClaimed is a repository another project owns.
	ErrRepositoryClaimed = errors.New("register: repository belongs to another project")

	// ErrNotLinked is a project id nobody has claimed, or a repository no
	// project owns.
	ErrNotLinked = errors.New("register: not linked")

	// ErrNoRepository is a link naming no configuration repository.
	ErrNoRepository = errors.New("register: a link names no configuration repository")

	// ErrNoClaimant is a link with nobody behind it.
	ErrNoClaimant = errors.New("register: a link names no claimant")
)

// Link is one claim: a project id, the configuration repository claiming it,
// the code repositories it attaches, and who asked.
type Link struct {
	Project  string
	Config   string
	Code     []string
	Claimant string
}

// ProjectRecord is what the register holds about a linked project, as every
// node resolves it.
type ProjectRecord struct {
	ID     string
	Config string
	// Code is the code repositories this project owns: attached by its owning
	// claim, and not owned by an earlier project.
	Code      []string
	Claimant  string
	CreatedAt time.Time
}

// Repositories is every repository that gates the project, configuration
// first.
func (p ProjectRecord) Repositories() []string {
	return append([]string{p.Config}, p.Code...)
}

// RepositoryRecord says which project a repository belongs to, and whether it
// is that project's configuration.
type RepositoryRecord struct {
	Project    string
	Repository string
	Configures bool
}

type claimRow struct {
	At       int64  `json:"at"`
	Claimant string `json:"claimant,omitempty"`
	Config   string `json:"config,omitempty"`
}

// NormaliseRepository is the spelling a repository key is stored under.
//
// Forges treat owner and name case-insensitively, so `Acme/Shop` and
// `acme/shop` are one repository and must be one key, or every uniqueness
// property built on it quietly fails.
func NormaliseRepository(key string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(key), "/"))
}

func segment(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func unsegment(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err
}

func claimKey(project, config string) string { return claimPrefix + project + sep + segment(config) }

func codeKey(project, config, code string) string {
	return codePrefix + project + sep + segment(config) + sep + segment(code)
}

func repoKey(repository, project string) string {
	return repoPrefix + segment(repository) + sep + project
}

func mineKey(claimant, project string) string { return minePrefix + segment(claimant) + sep + project }

// LinkProject claims l.Project for l.Config and attaches l.Code.
//
// A RELINK IS NOT A NEW CLAIM. A claim already held keeps the time it was
// first made, so linking again — to attach another code repository, or after
// a lost response — never moves a project later in the order.
//
// The checks before the writes are what turn the common, uncontended case into
// a clear refusal. They guarantee nothing under concurrency (another node can
// write between the read and the write); the resolution rule is what makes
// that outcome safe, and the final read reports it when it is already visible.
func (r *Register) LinkProject(ctx context.Context, l Link) (ProjectRecord, error) {
	if err := checkName(l.Project, ErrNoProject); err != nil {
		return ProjectRecord{}, err
	}
	config := NormaliseRepository(l.Config)
	if config == "" {
		return ProjectRecord{}, ErrNoRepository
	}
	claimant := strings.TrimSpace(l.Claimant)
	if claimant == "" {
		return ProjectRecord{}, ErrNoClaimant
	}
	var code []string
	for _, c := range l.Code {
		if c = NormaliseRepository(c); c != "" && c != config && !slices.Contains(code, c) {
			code = append(code, c)
		}
	}
	slices.Sort(code)

	r.mu.Lock()
	defer r.mu.Unlock()

	owner, found, err := r.owner(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if found && owner.Config != config {
		return ProjectRecord{}, claimedBy(l.Project, owner.Config)
	}
	for _, repo := range append([]string{config}, code...) {
		rec, held, err := r.repository(ctx, repo)
		if err != nil {
			return ProjectRecord{}, err
		}
		if held && rec.Project != l.Project {
			return ProjectRecord{}, fmt.Errorf("%w: %s is project %s's", ErrRepositoryClaimed, repo, rec.Project)
		}
	}

	at := r.now().UnixNano()
	if found {
		at = owner.CreatedAt.UnixNano()
	}

	// THE CLAIM FIRST, so the repository rows below are valid the moment they
	// land: a repository row only counts when its project's owning claim names
	// the same configuration repository.
	if err := r.putOnce(ctx, claimKey(l.Project, config), claimRow{At: at, Claimant: claimant}); err != nil {
		return ProjectRecord{}, err
	}
	if err := r.putOnce(ctx, repoKey(config, l.Project), claimRow{At: at, Config: config}); err != nil {
		return ProjectRecord{}, err
	}
	for _, c := range code {
		if err := r.putOnce(ctx, codeKey(l.Project, config, c), claimRow{At: at}); err != nil {
			return ProjectRecord{}, err
		}
		if err := r.putOnce(ctx, repoKey(c, l.Project), claimRow{At: at, Config: config}); err != nil {
			return ProjectRecord{}, err
		}
	}
	if err := r.putOnce(ctx, mineKey(claimant, l.Project), claimRow{At: at, Config: config}); err != nil {
		return ProjectRecord{}, err
	}

	rec, err := r.project(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if rec.Config != config {
		return ProjectRecord{}, claimedBy(l.Project, rec.Config)
	}
	for _, c := range code {
		if !slices.Contains(rec.Code, c) {
			held, _, err := r.repository(ctx, c)
			if err != nil {
				return ProjectRecord{}, err
			}
			return ProjectRecord{}, fmt.Errorf("%w: %s is project %s's", ErrRepositoryClaimed, c, held.Project)
		}
	}
	return rec, nil
}

func claimedBy(project, config string) error {
	return fmt.Errorf("%w: project %s is already claimed by %s; generate a new id in project.yaml",
		ErrProjectClaimed, project, config)
}

// putOnce writes a row that is absent and leaves one that is present: every
// row here is a fact about a moment, and rewriting it would move the moment.
func (r *Register) putOnce(ctx context.Context, key string, row claimRow) error {
	_, found, err := r.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("register: reading %s: %w", key, err)
	}
	if found {
		return nil
	}
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, key, b); err != nil {
		return fmt.Errorf("register: writing %s: %w", key, err)
	}
	return nil
}

// Project is a linked project as every node resolves it.
func (r *Register) Project(ctx context.Context, id string) (ProjectRecord, error) {
	if err := checkName(id, ErrNoProject); err != nil {
		return ProjectRecord{}, err
	}
	return r.project(ctx, id)
}

func (r *Register) project(ctx context.Context, id string) (ProjectRecord, error) {
	owner, found, err := r.owner(ctx, id)
	if err != nil {
		return ProjectRecord{}, err
	}
	if !found {
		return ProjectRecord{}, fmt.Errorf("%w: project %s", ErrNotLinked, id)
	}

	prefix := codePrefix + id + sep + segment(owner.Config) + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("register: listing %s's code: %w", id, err)
	}
	for _, key := range keys {
		_, rest, ok := strings.Cut(key, prefix)
		if !ok || strings.Contains(rest, sep) {
			continue
		}
		code, err := unsegment(rest)
		if err != nil {
			continue
		}
		// A CODE REPOSITORY ATTACHED HERE AND OWNED ELSEWHERE IS NOT THIS
		// PROJECT'S: two projects attaching one repository resolve like two
		// claims on one id.
		held, ok, err := r.repository(ctx, code)
		if err != nil {
			return ProjectRecord{}, err
		}
		if ok && held.Project == id {
			owner.Code = append(owner.Code, code)
		}
	}
	slices.Sort(owner.Code)
	return owner, nil
}

// owner is the claim on id that wins: earliest time, then smallest repository.
func (r *Register) owner(ctx context.Context, id string) (ProjectRecord, bool, error) {
	prefix := claimPrefix + id + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return ProjectRecord{}, false, fmt.Errorf("register: listing claims on %s: %w", id, err)
	}

	var best ProjectRecord
	found := false
	for _, key := range keys {
		_, rest, ok := strings.Cut(key, prefix)
		if !ok || strings.Contains(rest, sep) {
			continue
		}
		config, err := unsegment(rest)
		if err != nil {
			continue
		}
		row, ok, err := r.row(ctx, claimKey(id, config))
		if err != nil {
			return ProjectRecord{}, false, err
		}
		if !ok {
			continue
		}
		at := time.Unix(0, row.At).UTC()
		if !found || at.Before(best.CreatedAt) || (at.Equal(best.CreatedAt) && config < best.Config) {
			best = ProjectRecord{ID: id, Config: config, Claimant: row.Claimant, CreatedAt: at}
			found = true
		}
	}
	return best, found, nil
}

// row reads one record. A row that is present and unreadable is skipped rather
// than fatal: it cannot be ordered, so it cannot own anything, and refusing
// every read of the project over it would let one bad write deny the owner.
func (r *Register) row(ctx context.Context, key string) (claimRow, bool, error) {
	b, found, err := r.store.Get(ctx, key)
	if err != nil {
		return claimRow{}, false, fmt.Errorf("register: reading %s: %w", key, err)
	}
	if !found {
		return claimRow{}, false, nil
	}
	var row claimRow
	if json.Unmarshal(b, &row) != nil {
		return claimRow{}, false, nil
	}
	return row, true, nil
}

// Repository says which project owns a repository.
func (r *Register) Repository(ctx context.Context, repository string) (RepositoryRecord, error) {
	repository = NormaliseRepository(repository)
	if repository == "" {
		return RepositoryRecord{}, ErrNoRepository
	}
	rec, found, err := r.repository(ctx, repository)
	if err != nil {
		return RepositoryRecord{}, err
	}
	if !found {
		return RepositoryRecord{}, fmt.Errorf("%w: repository %s", ErrNotLinked, repository)
	}
	return rec, nil
}

// repository resolves a repository's owner: of the projects that claim it,
// ordered by (time, project), the first whose own owning claim is the one that
// attached it. A row written by a claim that lost its project id therefore
// never owns a repository.
func (r *Register) repository(ctx context.Context, repository string) (RepositoryRecord, bool, error) {
	prefix := repoPrefix + segment(repository) + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return RepositoryRecord{}, false, fmt.Errorf("register: listing claims on %s: %w", repository, err)
	}

	type candidate struct {
		project string
		row     claimRow
	}
	var candidates []candidate
	for _, key := range keys {
		_, project, ok := strings.Cut(key, prefix)
		if !ok || project == "" || strings.Contains(project, sep) {
			continue
		}
		row, ok, err := r.row(ctx, repoKey(repository, project))
		if err != nil {
			return RepositoryRecord{}, false, err
		}
		if ok {
			candidates = append(candidates, candidate{project, row})
		}
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.row.At != b.row.At {
			if a.row.At < b.row.At {
				return -1
			}
			return 1
		}
		return strings.Compare(a.project, b.project)
	})

	for _, c := range candidates {
		owner, found, err := r.owner(ctx, c.project)
		if err != nil {
			return RepositoryRecord{}, false, err
		}
		if found && owner.Config == c.row.Config {
			return RepositoryRecord{Project: c.project, Repository: repository, Configures: repository == owner.Config}, true, nil
		}
	}
	return RepositoryRecord{}, false, nil
}

// ProjectsOf is every project a claimant linked through the repository that
// owns it, sorted.
func (r *Register) ProjectsOf(ctx context.Context, claimant string) ([]ProjectRecord, error) {
	claimant = strings.TrimSpace(claimant)
	if claimant == "" {
		return nil, ErrNoClaimant
	}
	prefix := minePrefix + segment(claimant) + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing %s's projects: %w", claimant, err)
	}
	var out []ProjectRecord
	for _, key := range keys {
		_, id, ok := strings.Cut(key, prefix)
		if !ok || id == "" || strings.Contains(id, sep) {
			continue
		}
		row, ok, err := r.row(ctx, mineKey(claimant, id))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rec, err := r.project(ctx, id)
		if errors.Is(err, ErrNotLinked) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// A claim this claimant made and lost is not theirs; a relink of the
		// owning repository by a second maintainer is.
		if rec.Config == row.Config {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(a, b ProjectRecord) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
