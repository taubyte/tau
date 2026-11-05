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

// The project table: which project id belongs to which repositories, written when a
// developer links a project. The id is the author's (`id` in project.yaml); this
// table decides who may publish under it: the configuration repository that claimed
// it first.
//
// Every key sits under recordsPrefix ("r/"); publications own o/, p/, h/, c/, m/ and
// d/, so neither table can overwrite the other. A deploy reads this table to check
// the compiled id is claimed by the repository pushed to (services/plm/deploy).
//
//	r/claim/<project>/<config>          {at, claimant}  one per (project, config repository)
//	r/code/<project>/<config>/<code>    {at}            one per code repository a claim attaches
//	r/repo/<repository>/<project>       {at, config}    one per (repository, project) that claims it
//	r/mine/<claimant>/<project>         {at, config}    one per (claimant, project): "my projects"
//
// <config>, <code>, <repository> and <claimant> are base64url of the normalised
// value, since a repository key `host/owner/name` holds separators.
//
// No row is contended. The store is last-write-wins per key, so two nodes linking
// one id from different repositories write different keys and both claims survive.
// The owner is decided on read, identically everywhere: earliest `at`, then the
// smaller repository key. The same rule decides a repository two projects attach.

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
	// Replace makes the link the project's whole set of repositories: an
	// owner relinking after a rename, a transfer or a dropped code repository.
	Replace bool
}

// ProjectRecord is what the register holds about a linked project, as every
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

// claimRow is every record's value. Repo is the repository as spelled when linked,
// which a forge compares against; the key holds the normalised form.
type claimRow struct {
	At       int64  `json:"at"`
	Claimant string `json:"claimant,omitempty"`
	Config   string `json:"config,omitempty"`
	Repo     string `json:"repo,omitempty"`
}

// NormaliseRepository is the spelling a repository key is stored under.
//
// Forges treat owner and name case-insensitively, so `Acme/Shop` and
// `acme/shop` are one repository and must be one key, or every uniqueness
// property built on it quietly fails.
func NormaliseRepository(key string) string { return strings.ToLower(spelling(key)) }

func spelling(key string) string { return strings.Trim(strings.TrimSpace(key), "/") }

// spelt is a row's repository as linked, or the normalised form for a row
// written without one.
func spelt(row claimRow, normalised string) string {
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
func (r *Register) LinkProject(ctx context.Context, l Link) (ProjectRecord, error) {
	if err := checkName(l.Project, ErrNoProject); err != nil {
		return ProjectRecord{}, err
	}
	configSpelt := spelling(l.Config)
	config := NormaliseRepository(l.Config)
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
		if n := NormaliseRepository(c); n != "" && n != config && !slices.Contains(code, n) {
			code = append(code, n)
			codeSpelt[n] = spelling(c)
		}
	}
	slices.Sort(code)

	r.mu.Lock()
	defer r.mu.Unlock()

	owner, found, err := r.owner(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if found && !l.Replace && NormaliseRepository(owner.Config) != config {
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
	if found && l.Replace {
		claimant = owner.Claimant
		if err := r.unlink(ctx, owner, config, code); err != nil {
			return ProjectRecord{}, err
		}
	}

	// The claim first, so the repository rows are valid the moment they land.
	write := r.putOnce
	if l.Replace {
		write = r.put
	}
	if err := write(ctx, claimKey(l.Project, config), claimRow{At: at, Claimant: claimant, Repo: configSpelt}); err != nil {
		return ProjectRecord{}, err
	}
	if err := write(ctx, repoKey(config, l.Project), claimRow{At: at, Config: config, Repo: configSpelt}); err != nil {
		return ProjectRecord{}, err
	}
	for _, c := range code {
		if err := write(ctx, codeKey(l.Project, config, c), claimRow{At: at, Repo: codeSpelt[c]}); err != nil {
			return ProjectRecord{}, err
		}
		if err := write(ctx, repoKey(c, l.Project), claimRow{At: at, Config: config, Repo: codeSpelt[c]}); err != nil {
			return ProjectRecord{}, err
		}
	}
	if err := write(ctx, mineKey(claimant, l.Project), claimRow{At: at, Config: config}); err != nil {
		return ProjectRecord{}, err
	}

	rec, err := r.project(ctx, l.Project)
	if err != nil {
		return ProjectRecord{}, err
	}
	if NormaliseRepository(rec.Config) != config {
		return ProjectRecord{}, claimedBy(l.Project, rec.Config)
	}
	owned := map[string]bool{}
	for _, c := range rec.Code {
		owned[NormaliseRepository(c)] = true
	}
	for _, c := range code {
		if !owned[c] {
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

// unlink tombstones what a replacing link no longer holds: every other claim
// on the id, and each repository the new set leaves out. The store has no
// Delete, so a row is released by writing it empty, which row reads as absent.
func (r *Register) unlink(ctx context.Context, owner ProjectRecord, config string, code []string) error {
	held, err := r.project(ctx, owner.ID)
	if err != nil {
		return err
	}
	keys, err := r.store.List(ctx, claimPrefix+owner.ID+sep)
	if err != nil {
		return fmt.Errorf("register: listing claims on %s: %w", owner.ID, err)
	}
	for _, key := range keys {
		// A store may list a key rooted ("/r/claim/…"), the way a datastore
		// spells it; the key is the one it was written under.
		key = strings.TrimPrefix(key, sep)
		if key != claimKey(owner.ID, config) {
			if err := r.tombstone(ctx, key); err != nil {
				return err
			}
		}
	}
	old := NormaliseRepository(owner.Config)
	keep := append([]string{config}, code...)
	for _, c := range held.Code {
		c = NormaliseRepository(c)
		if old != config || !slices.Contains(code, c) {
			if err := r.tombstone(ctx, codeKey(owner.ID, old, c)); err != nil {
				return err
			}
		}
		if !slices.Contains(keep, c) {
			if err := r.tombstone(ctx, repoKey(c, owner.ID)); err != nil {
				return err
			}
		}
	}
	if !slices.Contains(keep, old) {
		return r.tombstone(ctx, repoKey(old, owner.ID))
	}
	return nil
}

func (r *Register) tombstone(ctx context.Context, key string) error {
	if err := r.store.Put(ctx, key, nil); err != nil {
		return fmt.Errorf("register: releasing %s: %w", key, err)
	}
	return nil
}

func (r *Register) put(ctx context.Context, key string, row claimRow) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, key, b); err != nil {
		return fmt.Errorf("register: writing %s: %w", key, err)
	}
	return nil
}

// putOnce writes a row that is absent and leaves one that is present: every
// row here is a fact about a moment, and rewriting it would move the moment.
func (r *Register) putOnce(ctx context.Context, key string, row claimRow) error {
	v, found, err := r.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("register: reading %s: %w", key, err)
	}
	if found && len(v) > 0 {
		return nil
	}
	return r.put(ctx, key, row)
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

	prefix := codePrefix + id + sep + segment(NormaliseRepository(owner.Config)) + sep
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
		row, ok, err := r.row(ctx, codeKey(id, NormaliseRepository(owner.Config), code))
		if err != nil {
			return ProjectRecord{}, err
		}
		if !ok {
			continue
		}
		// A code repository attached here and owned elsewhere is not this project's.
		held, ok, err := r.repository(ctx, code)
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
		if !found || at.Before(best.CreatedAt) || (at.Equal(best.CreatedAt) && config < NormaliseRepository(best.Config)) {
			best = ProjectRecord{ID: id, Config: spelt(row, config), Claimant: row.Claimant, CreatedAt: at}
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
		if found && NormaliseRepository(owner.Config) == c.row.Config {
			return RepositoryRecord{Project: c.project, Repository: spelt(c.row, repository), Configures: repository == c.row.Config}, true, nil
		}
	}
	return RepositoryRecord{}, false, nil
}

// ProjectsOf is every project a user linked through the repository that owns
// it, or holds a live grant on, sorted.
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
		if NormaliseRepository(rec.Config) == row.Config {
			out = append(out, rec)
		}
	}
	granted, err := r.granted(ctx, claimant)
	if err != nil {
		return nil, err
	}
	for _, id := range granted {
		if slices.ContainsFunc(out, func(p ProjectRecord) bool { return p.ID == id }) {
			continue
		}
		rec, err := r.project(ctx, id)
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
func (r *Register) ListProjects(ctx context.Context) ([]ProjectRecord, error) {
	keys, err := r.store.List(ctx, claimPrefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing claims: %w", err)
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
		rec, err := r.project(ctx, id)
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
