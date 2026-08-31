package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// store.go is the kvdb key layout — the only file in this package that
// builds a key. Every other file asks it for a row or a listing rather than
// concatenating a path itself, so there is exactly one place that has to
// agree with itself about the shape of a key.
//
// A REPOSITORY'S KEY IS VARIABLE DEPTH. `host/owner/name` is three segments
// and a GitLab subgroup nests one deeper —
// `gitlab.com/acme/team/widget` has owner `acme/team` (pkg/git/repokey) — so
// nothing below may count path segments to find a boundary. Every listing
// here matches a fixed MARKER ROW by regex suffix instead (the same fix
// commit 3a2111da made to v1's `repositories.List`, carried forward as the
// pattern for projects and hooks too, neither of which had the bug because
// neither has a variable-depth key, but both listings are one more place
// "count segments" could have crept in): a marker row's SUFFIX is fixed even
// when the id ahead of it is not, so trimming a known prefix and a known
// suffix off a listed row's key leaves exactly the id, at any depth.

// parseRepoKey turns the wire form of a repository_key field — the bare
// canonical key, e.g. "github.com/acme/pay", with no scheme — into a
// repokey.Key. repokey.Parse needs a scheme to work with, so one is added
// here; this is exactly what v1's p2p handlers did
// (services/auth/api_hooks.go: `repokey.Parse("https://" + repoID)`), and it
// is safe here for the same reason it was safe there: repository_key is
// documented as carrying the canonical key (pkg/v2/proto/auth/v1/auth.proto's
// IDENTITY section), not an arbitrary clone URL, so it never legitimately
// carries a scheme of its own to collide with the one added here.
func parseRepoKey(raw string) (repokey.Key, error) {
	return repokey.Parse("https://" + raw)
}

// ─────────────────────────── repositories ───────────────────────────
//
//	/repositories/<key>/key            deploy key, PEM, also the marker row
//	/repositories/<key>/registered_by  the peer that last (re-)registered it
//	/repositories/<key>/project        project id, once linked by Create
//	/repositories/<key>/hooks/<id>     reverse index, value unused

const repositoriesPrefix = "/repositories/"

func repoRoot(key repokey.Key) string { return repositoriesPrefix + key.String() }

func repoKeyRow(key repokey.Key) string      { return repoRoot(key) + "/key" }
func repoOwnerRow(key repokey.Key) string    { return repoRoot(key) + "/registered_by" }
func repoProjectRow(key repokey.Key) string  { return repoRoot(key) + "/project" }
func repoHooksPrefix(key repokey.Key) string { return repoRoot(key) + "/hooks/" }
func repoHookRow(key repokey.Key, hookID string) string {
	return repoHooksPrefix(key) + hookID
}

// repositoryKeyRowSuffix is repoKeyRow's fixed tail, used to find the
// marker row inside a listing without knowing the key's depth in advance.
const repositoryKeyRowSuffix = "/key"

// listRepositoryKeys returns the canonical key of every registered
// repository, derived from the /key marker row rather than a raw prefix
// listing: a raw listing over /repositories/ also returns every /project row
// and every /hooks/<id> row filed beside it, and stripping the prefix off
// those would read back as repositories that were never registered.
func listRepositoryKeys(ctx context.Context, kv kvdb.KVDB) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, repositoriesPrefix, regexp.QuoteMeta(repositoryKeyRowSuffix)+"$")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, strings.TrimSuffix(strings.TrimPrefix(row, repositoriesPrefix), repositoryKeyRowSuffix))
	}
	return keys, nil
}

// repositoryRecord is what the store holds for one registered repository.
type repositoryRecord struct {
	deployKey string
	project   string // empty until a Create links it
	owner     peer.ID
}

// fetchRepository reads a registered repository's row, or an error if key
// was never registered.
func fetchRepository(ctx context.Context, kv kvdb.KVDB, key repokey.Key) (repositoryRecord, error) {
	deployKey, err := kv.Get(ctx, repoKeyRow(key))
	if err != nil || deployKey == nil {
		return repositoryRecord{}, fmt.Errorf("repository %q is not registered", key)
	}
	project, _ := kv.Get(ctx, repoProjectRow(key))
	owner, _ := kv.Get(ctx, repoOwnerRow(key))
	return repositoryRecord{
		deployKey: string(deployKey),
		project:   string(project),
		owner:     peer.ID(owner),
	}, nil
}

// repositoryOwner reads who is currently allowed to mutate key, and whether
// key is registered at all. It is the read half of the ownership check
// authz.go enforces on Register (of an already-registered key) and
// Unregister.
func repositoryOwner(ctx context.Context, kv kvdb.KVDB, key repokey.Key) (owner peer.ID, registered bool) {
	deployKey, err := kv.Get(ctx, repoKeyRow(key))
	if err != nil || deployKey == nil {
		return "", false
	}
	raw, _ := kv.Get(ctx, repoOwnerRow(key))
	return peer.ID(raw), true
}

// registerRepository generates a fresh deploy key and files it under key,
// recording caller as the peer allowed to touch it again. It is called only
// after authz.go has confirmed the caller may do this — either key was never
// registered, or caller already owns it.
func registerRepository(ctx context.Context, kv kvdb.KVDB, key repokey.Key, by peer.ID) error {
	deployKey, err := generateDeployKey()
	if err != nil {
		return fmt.Errorf("generating deploy key: %w", err)
	}

	batch, err := kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}
	if err := batch.Put(repoKeyRow(key), []byte(deployKey)); err != nil {
		return fmt.Errorf("batching deploy key: %w", err)
	}
	if err := batch.Put(repoOwnerRow(key), []byte(by)); err != nil {
		return fmt.Errorf("batching registrant: %w", err)
	}
	return batch.Commit()
}

// deleteRepository removes every row a repository owns, including every
// hook filed under it — the /project link (v1's bug: commit 3a2111da fixed
// Delete leaving /project behind forever, see repositories.go) and every
// hook's own rows, not just its reverse-index pointer, so nothing survives
// under a key that no longer exists.
func deleteRepository(ctx context.Context, kv kvdb.KVDB, key repokey.Key) error {
	hookRows, err := kv.List(ctx, repoHooksPrefix(key))
	if err != nil {
		return fmt.Errorf("listing hooks: %w", err)
	}

	batch, err := kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}

	for _, row := range hookRows {
		id := strings.TrimPrefix(row, repoHooksPrefix(key))
		if err := batchDeleteHook(batch, id); err != nil {
			return err
		}
		if err := batch.Delete(row); err != nil {
			return fmt.Errorf("batching hook reverse index delete: %w", err)
		}
	}

	if err := batch.Delete(repoKeyRow(key)); err != nil {
		return fmt.Errorf("batching repository key delete: %w", err)
	}
	if err := batch.Delete(repoOwnerRow(key)); err != nil {
		return fmt.Errorf("batching registrant delete: %w", err)
	}
	if err := batch.Delete(repoProjectRow(key)); err != nil {
		return fmt.Errorf("batching project link delete: %w", err)
	}

	return batch.Commit()
}

// generateDeployKey produces a fresh P-256 private key, PEM-encoded, the
// same shape services/auth/github.go's generateKey produces for `kpriv`. v1
// also derived an SSH authorized_keys-format public key there, for a
// CreateDeployKey call against GitHub's API; that call belongs to the
// GitHub-token HTTP surface this schema does not model (see auth.proto's
// package doc), so only the half this store actually hands back — the
// private key — is generated here.
func generateDeployKey() (string, error) {
	private, _, err := generateECKeyPair()
	return private, err
}

// generateECKeyPair returns a fresh P-256 key pair as PEM, in the exact
// encoding github.com/taubyte/domain-validation's PrivateKey/PublicKey
// options parse (pkg/config/source_load.go's generatePublicKeyFromPEM is the
// same construction run in reverse: EC PRIVATE KEY / PUBLIC KEY PEM blocks
// around x509's EC private key and PKIX public key DER). One function
// produces both halves because both consumers in this file — the deploy key
// (private only) and, in tests, a domain-validation key pair (both halves)
// — need the same key shape.
func generateECKeyPair() (privatePEM, publicPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	privDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	privatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}))

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	return privatePEM, publicPEM, nil
}

// ────────────────────────────── projects ──────────────────────────────
//
//	/projects/<id>/name
//	/projects/<id>/config   config repository's canonical key
//	/projects/<id>/code     code repository's canonical key

const projectsPrefix = "/projects/"

func projectRoot(id string) string      { return projectsPrefix + id }
func projectNameRow(id string) string   { return projectRoot(id) + "/name" }
func projectConfigRow(id string) string { return projectRoot(id) + "/config" }
func projectCodeRow(id string) string   { return projectRoot(id) + "/code" }

const projectNameRowSuffix = "/name"

// listProjectIDs mirrors listRepositoryKeys: it matches the /name marker row
// rather than a raw prefix listing, so a future per-project row (an owners
// list, say — v1 has one, /projects/<id>/owners/<...>, out of scope here)
// cannot make a project id appear more than once, or appear where none was
// ever registered.
func listProjectIDs(ctx context.Context, kv kvdb.KVDB) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, projectsPrefix, regexp.QuoteMeta(projectNameRowSuffix)+"$")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(row, projectsPrefix), projectNameRowSuffix))
	}
	return ids, nil
}

type projectRecord struct {
	name   string
	config string
	code   string
}

func fetchProject(ctx context.Context, kv kvdb.KVDB, id string) (projectRecord, error) {
	name, err := kv.Get(ctx, projectNameRow(id))
	if err != nil || name == nil {
		return projectRecord{}, fmt.Errorf("project %q not found", id)
	}
	config, _ := kv.Get(ctx, projectConfigRow(id))
	code, _ := kv.Get(ctx, projectCodeRow(id))
	return projectRecord{name: string(name), config: string(config), code: string(code)}, nil
}

func putProject(ctx context.Context, kv kvdb.KVDB, id, name string, config, code repokey.Key) error {
	batch, err := kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}
	if err := batch.Put(projectNameRow(id), []byte(name)); err != nil {
		return fmt.Errorf("batching project name: %w", err)
	}
	if err := batch.Put(projectConfigRow(id), []byte(config.String())); err != nil {
		return fmt.Errorf("batching config repository: %w", err)
	}
	if err := batch.Put(projectCodeRow(id), []byte(code.String())); err != nil {
		return fmt.Errorf("batching code repository: %w", err)
	}
	return batch.Commit()
}

// linkRepositoryToProject files the project a repository belongs to. It is
// written unconditionally, the same way v1's createProjectStream did
// (services/auth/api_hooks.go), whether or not key was ever registered as a
// repository: a bare /project row with no /key beside it does not appear in
// listRepositoryKeys (which matches /key, not /project), so it cannot be
// mistaken for a registered repository later.
func linkRepositoryToProject(ctx context.Context, kv kvdb.KVDB, key repokey.Key, projectID string) error {
	return kv.Put(ctx, repoProjectRow(key), []byte(projectID))
}

// ──────────────────────────────── hooks ────────────────────────────────
//
//	/hooks/<id>/provider          "github" — also the marker row
//	/hooks/<id>/github/id         GitHub's own numeric webhook id, decimal
//	/hooks/<id>/github/secret
//	/hooks/<id>/github/repository the repository's canonical key

const hooksPrefix = "/hooks/"

func hookRoot(id string) string            { return hooksPrefix + id }
func hookProviderRow(id string) string     { return hookRoot(id) + "/provider" }
func hookGithubIDRow(id string) string     { return hookRoot(id) + "/github/id" }
func hookGithubSecretRow(id string) string { return hookRoot(id) + "/github/secret" }
func hookGithubRepoRow(id string) string   { return hookRoot(id) + "/github/repository" }

const hookProviderRowSuffix = "/provider"

func listHookIDs(ctx context.Context, kv kvdb.KVDB) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, hooksPrefix, regexp.QuoteMeta(hookProviderRowSuffix)+"$")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(row, hooksPrefix), hookProviderRowSuffix))
	}
	return ids, nil
}

type githubHookRecord struct {
	providerHookID int64
	secret         string
	repository     string
}

// fetchHookProvider reads which provider a hook was filed under, or an error
// if id was never registered.
func fetchHookProvider(ctx context.Context, kv kvdb.KVDB, id string) (string, error) {
	provider, err := kv.Get(ctx, hookProviderRow(id))
	if err != nil || provider == nil {
		return "", fmt.Errorf("hook %q not found", id)
	}
	return string(provider), nil
}

func fetchGithubHook(ctx context.Context, kv kvdb.KVDB, id string) (githubHookRecord, error) {
	rawID, err := kv.Get(ctx, hookGithubIDRow(id))
	if err != nil {
		return githubHookRecord{}, err
	}
	providerHookID, err := strconv.ParseInt(string(rawID), 10, 64)
	if err != nil {
		return githubHookRecord{}, fmt.Errorf("hook %q's provider id %q is not a number: %w", id, rawID, err)
	}
	secret, err := kv.Get(ctx, hookGithubSecretRow(id))
	if err != nil {
		return githubHookRecord{}, err
	}
	repository, err := kv.Get(ctx, hookGithubRepoRow(id))
	if err != nil {
		return githubHookRecord{}, err
	}
	return githubHookRecord{
		providerHookID: providerHookID,
		secret:         string(secret),
		repository:     string(repository),
	}, nil
}

// batchDeleteHook queues every row a hook owns, whatever its provider. There
// is only one provider today (github; bitbucket is reachable in the schema
// and unimplemented everywhere, per auth.proto's own doc on BitbucketHook),
// so this only has one case to batch, but it is written to be extended by
// provider rather than assumed to be github forever.
func batchDeleteHook(batch kvdb.Batch, id string) error {
	if err := batch.Delete(hookProviderRow(id)); err != nil {
		return fmt.Errorf("batching hook provider delete: %w", err)
	}
	if err := batch.Delete(hookGithubIDRow(id)); err != nil {
		return fmt.Errorf("batching hook id delete: %w", err)
	}
	if err := batch.Delete(hookGithubSecretRow(id)); err != nil {
		return fmt.Errorf("batching hook secret delete: %w", err)
	}
	if err := batch.Delete(hookGithubRepoRow(id)); err != nil {
		return fmt.Errorf("batching hook repository delete: %w", err)
	}
	return nil
}

// ───────────────────────────── certificates ─────────────────────────────
//
//	/acme/<b64 fqdn>/certificate/pem      ACME-issued, autocert-managed
//	/static/<b64 fqdn>/certificate/pem    operator-injected
//	/acme/cache/<b64 key>/data            autocert's own opaque cache

func acmeCertRow(fqdn string) string   { return "/acme/" + b64(fqdn) + "/certificate/pem" }
func staticCertRow(fqdn string) string { return "/static/" + b64(fqdn) + "/certificate/pem" }
func cacheDataRow(key string) string   { return "/acme/cache/" + b64(key) + "/data" }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// wildcardFQDN replaces fqdn's leftmost label with "*", the same
// transformation services/auth/helpers.go's generateWildCardDomain makes:
// "www.example.com" -> "*.example.com". A single-label fqdn becomes "*".
func wildcardFQDN(fqdn string) string {
	split := strings.SplitAfterN(fqdn, ".", 2)
	split[0] = "*"
	return strings.Join(split, ".")
}
