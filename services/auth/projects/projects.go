package projects

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// The project table: which project id belongs to which repositories, written when a
// developer links a project. The id is the author's (`id` in project.yaml); this
// table decides who may publish under it: the configuration repository that claimed
// it first.
//
// Every key sits under recordsPrefix ("/projects/") in auth's store, beside its
// users. A deploy reads this table to check the compiled id is claimed by the
// repository pushed to (services/plm/deploy). Each value is a ProjectClaimRow
// (store.proto):
//
//	/projects/claim/<project>/<config>          {at, claimant}  one per (project, config repository)
//	/projects/code/<project>/<config>/<code>    {at}            one per code repository a claim attaches
//	/projects/repo/<repository>/<project>       {at, config}    one per (repository, project) that claims it
//	/projects/mine/<claimant>/<project>         {at, config}    one per (claimant, project): "my projects"
//
// <config>, <code>, <repository> and <claimant> are base64url of the normalised
// value, since a repository key `host/owner/name` holds separators.
//
// No row is contended. The store is last-write-wins per key, so two nodes linking
// one id from different repositories write different keys and both claims survive.
// The owner is decided on read, identically everywhere: earliest `at`, then the
// smaller repository key. The same rule decides a repository two projects attach.

const (
	recordsPrefix = sep + "projects" + sep
	claimPrefix   = recordsPrefix + "claim" + sep
	codePrefix    = recordsPrefix + "code" + sep
	repoPrefix    = recordsPrefix + "repo" + sep
	minePrefix    = recordsPrefix + "mine" + sep
)

var (
	// ErrProjectClaimed is a project id another configuration repository owns.
	// The fix is the author's: project.yaml's id is theirs to regenerate.
	ErrProjectClaimed = errors.New("auth: project id is already claimed")

	// ErrRepositoryClaimed is a repository another project owns.
	ErrRepositoryClaimed = errors.New("auth: repository belongs to another project")

	// ErrNotLinked is a project id nobody has claimed, or a repository no
	// project owns.
	ErrNotLinked = errors.New("auth: not linked")

	// ErrNoRepository is a link naming no configuration repository.
	ErrNoRepository = errors.New("auth: a link names no configuration repository")

	// ErrNoClaimant is a link with nobody behind it.
	ErrNoClaimant = errors.New("auth: a link names no claimant")
)

// Link is one claim: a project id, the configuration repository claiming it,
// the code repositories it attaches, and who asked.
type Link struct {
	Project  string
	Config   string
	Code     []string
	Claimant string
	// Replace makes the link the project's whole set of repositories: an
	// owner relinking after a rename, a transfer or a dropped code repository.
	Replace bool
}

// ProjectRecord is what the table holds about a linked project, as every
// node resolves it. Repositories are as they were spelled when linked.
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

func spelling(key string) string { return strings.Trim(strings.TrimSpace(key), "/") }

// spelt is a row's repository as linked, or the normalised form for a row
// written without one.
func spelt(row *authv2.ProjectClaimRow, normalised string) string {
	if row.Repo != "" {
		return row.Repo
	}
	return normalised
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

// LinkProject claims l.Project for l.Config and attaches l.Code. A relink keeps the
// claim's original time, so it never moves a project later in the order.
//
// The checks before the writes give a clear refusal in the uncontended case and
// guarantee nothing under concurrency; the resolution rule makes that safe.
func (t *Table) LinkProject(ctx context.Context, l Link) (ProjectRecord, error) {
	if err := checkName(l.Project, ErrNoProject); err != nil {
		return ProjectRecord{}, err
	}
	configSpelt := spelling(l.Config)
	config := repokey.Normalise(l.Config)
	if config == "" {
		return ProjectRecord{}, ErrNoRepository
	}
	claimant := strings.TrimSpace(l.Claimant)
	if claimant == "" {
		return ProjectRecord{}, ErrNoClaimant
	}
	var code []string
	codeSpelt := map[string]string{}
	for _, c := range l.Code {
		if n := repokey.Normalise(c); n != "" && n != config && !slices.Contains(code, n) {
			code = append(code, n)
			codeSpelt[n] = spelling(c)
		}
	}
	slices.Sort(code)

	t.mu.Lock()
	defer t.mu.Unlock()

	owner, found, err := t.owner(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if found && !l.Replace && repokey.Normalise(owner.Config) != config {
		return ProjectRecord{}, claimedBy(l.Project, owner.Config)
	}
	for _, repo := range append([]string{config}, code...) {
		rec, held, err := t.repository(ctx, repo)
		if err != nil {
			return ProjectRecord{}, err
		}
		if held && rec.Project != l.Project {
			return ProjectRecord{}, fmt.Errorf("%w: %s is project %s's", ErrRepositoryClaimed, repo, rec.Project)
		}
	}

	at := t.now().UnixNano()
	if found {
		at = owner.CreatedAt.UnixNano()
	}
	if found && l.Replace {
		claimant = owner.Claimant
		if err := t.unlink(ctx, owner, config, code); err != nil {
			return ProjectRecord{}, err
		}
	}

	// The claim first, so the repository rows are valid the moment they land.
	write := t.putOnce
	if l.Replace {
		write = func(ctx context.Context, key string, row *authv2.ProjectClaimRow) error { return t.put(ctx, key, row) }
	}
	if err := write(ctx, claimKey(l.Project, config), &authv2.ProjectClaimRow{At: at, Claimant: claimant, Repo: configSpelt}); err != nil {
		return ProjectRecord{}, err
	}
	if err := write(ctx, repoKey(config, l.Project), &authv2.ProjectClaimRow{At: at, Config: config, Repo: configSpelt}); err != nil {
		return ProjectRecord{}, err
	}
	for _, c := range code {
		if err := write(ctx, codeKey(l.Project, config, c), &authv2.ProjectClaimRow{At: at, Repo: codeSpelt[c]}); err != nil {
			return ProjectRecord{}, err
		}
		if err := write(ctx, repoKey(c, l.Project), &authv2.ProjectClaimRow{At: at, Config: config, Repo: codeSpelt[c]}); err != nil {
			return ProjectRecord{}, err
		}
	}
	if err := write(ctx, mineKey(claimant, l.Project), &authv2.ProjectClaimRow{At: at, Config: config}); err != nil {
		return ProjectRecord{}, err
	}

	rec, err := t.project(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if repokey.Normalise(rec.Config) != config {
		return ProjectRecord{}, claimedBy(l.Project, rec.Config)
	}
	owned := map[string]bool{}
	for _, c := range rec.Code {
		owned[repokey.Normalise(c)] = true
	}
	for _, c := range code {
		if !owned[c] {
			held, _, err := t.repository(ctx, c)
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

// unlink tombstones what a replacing link no longer holds: every other claim
// on the id, and each repository the new set leaves out. The store has no
// Delete, so a row is released by writing it empty, which row reads as absent.
func (t *Table) unlink(ctx context.Context, owner ProjectRecord, config string, code []string) error {
	held, err := t.project(ctx, owner.ID)
	if err != nil {
		return err
	}
	keys, err := t.store.List(ctx, claimPrefix+owner.ID+sep)
	if err != nil {
		return fmt.Errorf("auth: listing claims on %s: %w", owner.ID, err)
	}
	for _, key := range keys {
		if key != claimKey(owner.ID, config) {
			if err := t.tombstone(ctx, key); err != nil {
				return err
			}
		}
	}
	old := repokey.Normalise(owner.Config)
	keep := append([]string{config}, code...)
	for _, c := range held.Code {
		c = repokey.Normalise(c)
		if old != config || !slices.Contains(code, c) {
			if err := t.tombstone(ctx, codeKey(owner.ID, old, c)); err != nil {
				return err
			}
		}
		if !slices.Contains(keep, c) {
			if err := t.tombstone(ctx, repoKey(c, owner.ID)); err != nil {
				return err
			}
		}
	}
	if !slices.Contains(keep, old) {
		return t.tombstone(ctx, repoKey(old, owner.ID))
	}
	return nil
}

func (t *Table) tombstone(ctx context.Context, key string) error {
	if err := t.store.Put(ctx, key, nil); err != nil {
		return fmt.Errorf("auth: releasing %s: %w", key, err)
	}
	return nil
}

// putOnce writes a row that is absent and leaves one that is present: every
// row here is a fact about a moment, and rewriting it would move the moment.
func (t *Table) putOnce(ctx context.Context, key string, row *authv2.ProjectClaimRow) error {
	v, found, err := t.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("auth: reading %s: %w", key, err)
	}
	if found && len(v) > 0 {
		return nil
	}
	return t.put(ctx, key, row)
}

// Project is a linked project as every node resolves it.
func (t *Table) Project(ctx context.Context, id string) (ProjectRecord, error) {
	if err := checkName(id, ErrNoProject); err != nil {
		return ProjectRecord{}, err
	}
	return t.project(ctx, id)
}

func (t *Table) project(ctx context.Context, id string) (ProjectRecord, error) {
	owner, found, err := t.owner(ctx, id)
	if err != nil {
		return ProjectRecord{}, err
	}
	if !found {
		return ProjectRecord{}, fmt.Errorf("%w: project %s", ErrNotLinked, id)
	}

	prefix := codePrefix + id + sep + segment(repokey.Normalise(owner.Config)) + sep
	keys, err := t.store.List(ctx, prefix)
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("auth: listing %s's code: %w", id, err)
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
		row, ok, err := t.row(ctx, codeKey(id, repokey.Normalise(owner.Config), code))
		if err != nil {
			return ProjectRecord{}, err
		}
		if !ok {
			continue
		}
		// A code repository attached here and owned elsewhere is not this project's.
		held, ok, err := t.repository(ctx, code)
		if err != nil {
			return ProjectRecord{}, err
		}
		if ok && held.Project == id {
			owner.Code = append(owner.Code, spelt(row, code))
		}
	}
	slices.Sort(owner.Code)
	return owner, nil
}

// owner is the claim on id that wins: earliest time, then smallest repository.
func (t *Table) owner(ctx context.Context, id string) (ProjectRecord, bool, error) {
	prefix := claimPrefix + id + sep
	keys, err := t.store.List(ctx, prefix)
	if err != nil {
		return ProjectRecord{}, false, fmt.Errorf("auth: listing claims on %s: %w", id, err)
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
		row, ok, err := t.row(ctx, claimKey(id, config))
		if err != nil {
			return ProjectRecord{}, false, err
		}
		if !ok {
			continue
		}
		at := time.Unix(0, row.At).UTC()
		if !found || at.Before(best.CreatedAt) || (at.Equal(best.CreatedAt) && config < repokey.Normalise(best.Config)) {
			best = ProjectRecord{ID: id, Config: spelt(row, config), Claimant: row.Claimant, CreatedAt: at}
			found = true
		}
	}
	return best, found, nil
}

// row reads one record; see read for what reads as absent.
func (t *Table) row(ctx context.Context, key string) (*authv2.ProjectClaimRow, bool, error) {
	row := new(authv2.ProjectClaimRow)
	found, err := t.read(ctx, key, row)
	return row, found, err
}

// Repository says which project owns a repository.
func (t *Table) Repository(ctx context.Context, repository string) (RepositoryRecord, error) {
	repository = repokey.Normalise(repository)
	if repository == "" {
		return RepositoryRecord{}, ErrNoRepository
	}
	rec, found, err := t.repository(ctx, repository)
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
func (t *Table) repository(ctx context.Context, repository string) (RepositoryRecord, bool, error) {
	prefix := repoPrefix + segment(repository) + sep
	keys, err := t.store.List(ctx, prefix)
	if err != nil {
		return RepositoryRecord{}, false, fmt.Errorf("auth: listing claims on %s: %w", repository, err)
	}

	type candidate struct {
		project string
		row     *authv2.ProjectClaimRow
	}
	var candidates []candidate
	for _, key := range keys {
		_, project, ok := strings.Cut(key, prefix)
		if !ok || project == "" || strings.Contains(project, sep) {
			continue
		}
		row, ok, err := t.row(ctx, repoKey(repository, project))
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
		owner, found, err := t.owner(ctx, c.project)
		if err != nil {
			return RepositoryRecord{}, false, err
		}
		if found && repokey.Normalise(owner.Config) == c.row.Config {
			return RepositoryRecord{Project: c.project, Repository: spelt(c.row, repository), Configures: repository == c.row.Config}, true, nil
		}
	}
	return RepositoryRecord{}, false, nil
}

// ProjectsOf is every project a user linked through the repository that owns
// it, or holds a live grant on, sorted.
func (t *Table) ProjectsOf(ctx context.Context, claimant string) ([]ProjectRecord, error) {
	claimant = strings.TrimSpace(claimant)
	if claimant == "" {
		return nil, ErrNoClaimant
	}
	prefix := minePrefix + segment(claimant) + sep
	keys, err := t.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("auth: listing %s's projects: %w", claimant, err)
	}
	var out []ProjectRecord
	for _, key := range keys {
		_, id, ok := strings.Cut(key, prefix)
		if !ok || id == "" || strings.Contains(id, sep) {
			continue
		}
		row, ok, err := t.row(ctx, mineKey(claimant, id))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rec, err := t.project(ctx, id)
		if errors.Is(err, ErrNotLinked) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// A claim this claimant made and lost is not theirs; a relink of the
		// owning repository by a second maintainer is.
		if repokey.Normalise(rec.Config) == row.Config {
			out = append(out, rec)
		}
	}
	granted, err := t.granted(ctx, claimant)
	if err != nil {
		return nil, err
	}
	for _, id := range granted {
		if slices.ContainsFunc(out, func(p ProjectRecord) bool { return p.ID == id }) {
			continue
		}
		rec, err := t.project(ctx, id)
		if errors.Is(err, ErrNotLinked) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b ProjectRecord) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// ListProjects is every linked project, sorted by id: the operator's view.
// Each id appears once however many claims it has, resolved like any other
// read.
func (t *Table) ListProjects(ctx context.Context) ([]ProjectRecord, error) {
	keys, err := t.store.List(ctx, claimPrefix)
	if err != nil {
		return nil, fmt.Errorf("auth: listing claims: %w", err)
	}
	seen := map[string]bool{}
	var out []ProjectRecord
	for _, key := range keys {
		_, rest, ok := strings.Cut(key, claimPrefix)
		if !ok {
			continue
		}
		id, _, ok := strings.Cut(rest, sep)
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		rec, err := t.project(ctx, id)
		if errors.Is(err, ErrNotLinked) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b ProjectRecord) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
