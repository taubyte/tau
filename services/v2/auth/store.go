package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"

	datastore "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// store.go is the kvdb key layout — the only file in this package that
// builds a key. Every other file asks it for a row or a listing rather than
// concatenating a path itself, so there is exactly one place that has to
// agree with itself about the shape of a key.
//
// ── THE GRAMMAR ──────────────────────────────────────────────────────────
//
// A key is a sequence of "/"-separated segments. A segment is either a
// LITERAL spelled out in this file, or an ID passed through segment().
// segment() is unpadded base64url (RFC 4648 §5), whose alphabet is
// A-Z a-z 0-9 - _ — so an encoded id NEVER contains "/" and is never "." or
// "..". Every boundary in every key is therefore at a fixed segment index,
// whatever the id was and however deep it reads.
//
// AN ID IS NON-EMPTY. That is the grammar's one precondition, and it is
// enforced at the edge (errors.go's requireID, and repokey.Parse for a
// repository key) rather than assumed: base64url of "" is "", and
// go-datastore removes an empty segment outright when it cleans a key
// (pkg/kvdb/methods.go passes every key through ds.NewKey). A removed
// segment is a moved boundary, which is exactly the class below.
//
// That is the whole invariant, and it is what every listing below rests on.
//
// ── WHY IT IS NOT DECORATION ─────────────────────────────────────────────
//
// A REPOSITORY'S KEY IS VARIABLE DEPTH. `github.com/acme/pay` is three
// segments and a GitLab subgroup nests one deeper —
// `gitlab.com/acme/team/widget` has owner `acme/team` (pkg/git/repokey).
// Spliced into a path raw, it eats the segment boundaries around it, and
// then NO sub-namespace reserved under it is safe: a legitimate repository
// can always be named so that its own rows land inside the prefix.
//
// The first pass spliced it raw and lost a repository to it.
// `repokey.Parse("https://gitlab.com/acme/team/hooks")` is an ordinary
// repository whose last segment happens to be "hooks", and its three rows
// sat at exactly the paths a listing of the HOOK REVERSE INDEX of the
// shallower key `gitlab.com/acme/team` returns. Registering that shallower
// key is open to anyone (nothing owns it yet; first writer wins), and
// unregistering it deleted the deeper repository's rows — /registered_by
// included. With the ownership row gone, authz.go had nothing left to
// check, so the next Register handed the attacker the victim's repository
// and a fresh deploy key. Full takeover, from two ordinary RPCs.
// authz_test.go's TestRepositoryTakeoverThroughANestedNamespaceIsImpossible
// is that attack, run over the wire by a second peer.
//
// The fix is the encoding rather than a guard in deleteRepository, because
// the encoding removes the CLASS: at every depth, for every id, including
// the ids this package does not generate and only ever receives from the
// wire (a hook id, an fqdn, an autocert cache key).
//
// ── AND EVERY LISTING IS ANCHORED ────────────────────────────────────────
//
// Every listing here matches a FULL anchored pattern with `[^/]+` where the
// id goes (idRowPattern) — v1's own mechanism, `/hooks/([^/]+)$` in
// services/auth/repositories/repositories.go:94, which the first pass
// replaced with a bare TrimPrefix.
//
// BE HONEST ABOUT WHAT THIS ADDS TODAY: nothing. The encoding alone is
// already sufficient, because a literal spelled out in this file is not
// something segment() can produce — base64url's alphabet refuses most of
// them and its canonical trailing bits refuse the rest, "key" included — so
// no non-marker row can end where a marker row ends. Remove the anchor and
// every test in this package still passes.
//
// It stays because that argument is exactly the kind this file must not
// depend on. Without `[^/]+`, listRepositoryKeys is correct only by way of
// base64 canonicity PLUS an inventory of every other row anyone ever files
// under /repositories/, and the next row added re-opens both silently — one
// leaf named to end in "/provider" under /hooks/, one metadata row under a
// hook, and a listing starts answering with rows that are not marker rows.
// The anchor makes each listing's correctness local to the listing: one
// segment where an id goes, whatever else is in the namespace. That is a
// contract, and store_test.go's TestIdRowPatternMatchesOneSegmentOnly tests
// it as one.
//
// The pattern matches the WHOLE row rather than its tail because that is
// what ListRegEx hands it: go-datastore returns full, leading-slash keys
// (pkg/kvdb/regexp_methods.go matches `e.Key`), which v1's own
// services/auth/repositories.List relies on too. Confirmed against the real
// CRDT backend, not just pkg/kvdb/mock: `^/repositories/[^/]+/key$` over a
// store holding a /key, a /registered_by and a /hooks/<id> row returns the
// marker row alone.
//
// The cost is that a key read out of the store by eye is opaque. That is
// paid once, by whoever is debugging; the alternative was paid by whoever
// registered a repository ending in a word this file also uses.

// segment encodes an id into exactly one path segment. See the grammar
// above: base64url's alphabet excludes "/", so the encoding of ANY string —
// a variable-depth repository key, an fqdn, an operator's autocert cache
// key — is one segment and cannot move a boundary.
//
// It is also why nothing here needs to care that go-datastore cleans the
// keys it is given (pkg/kvdb/methods.go passes every key through
// ds.NewKey): a "." or ".." segment would be cleaned away and could collide
// with a different row, and base64url can produce neither.
func segment(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// unsegment reverses segment. It fails on a row this package did not write,
// which is a corrupt store rather than something to skip quietly.
func unsegment(s string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("row segment %q was not written by this service: %w", s, err)
	}
	return string(raw), nil
}

// idRowPattern is the anchored full-row regex for `prefix<one id>suffix` —
// the only shape a listing in this file ever matches. `[^/]+` is what makes
// it anchored on both sides of the id even though the id's length is not
// known: it cannot cross a segment boundary, so a deeper row filed under
// the same prefix cannot answer it.
func idRowPattern(prefix, suffix string) string {
	return "^" + regexp.QuoteMeta(prefix) + "[^/]+" + regexp.QuoteMeta(suffix) + "$"
}

// idsFromRows turns the rows a listing returned back into the ids they were
// built from. It is the exact inverse of `prefix + segment(id) + suffix`,
// and it is one function so that no listing can invert its own keys
// slightly differently.
func idsFromRows(rows []string, prefix, suffix string) ([]string, error) {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id, err := unsegment(strings.TrimSuffix(strings.TrimPrefix(row, prefix), suffix))
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseRepoKey turns the wire form of a repository_key field — the bare
// canonical key, e.g. "github.com/acme/pay", with no scheme — into a
// repokey.Key. repokey.Parse needs a scheme to work with, so one is added
// here; this is exactly what v1's p2p handlers did
// (services/auth/api_hooks.go: `repokey.Parse("https://" + repoID)`), and it
// is safe here for the same reason it was safe there: repository_key is
// documented as carrying the canonical key (pkg/proto/tau/auth/v1/auth.proto's
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
//
// <key> and <id> are segment()-encoded, so `/hooks/` is a real boundary and
// not one a repository name can reach.

const repositoriesPrefix = "/repositories/"

func repoRoot(key repokey.Key) string { return repositoriesPrefix + segment(key.String()) }

func repoKeyRow(key repokey.Key) string      { return repoRoot(key) + repositoryKeyRowSuffix }
func repoOwnerRow(key repokey.Key) string    { return repoRoot(key) + "/registered_by" }
func repoProjectRow(key repokey.Key) string  { return repoRoot(key) + "/project" }
func repoHooksPrefix(key repokey.Key) string { return repoRoot(key) + "/hooks/" }
func repoHookRow(key repokey.Key, hookID string) string {
	return repoHooksPrefix(key) + segment(hookID)
}

// repositoryKeyRowSuffix is repoKeyRow's fixed tail, used to find the
// marker row inside a listing without knowing the id ahead of it.
const repositoryKeyRowSuffix = "/key"

// listRepositoryKeys returns the canonical key of every registered
// repository, derived from the /key marker row rather than a raw prefix
// listing: a raw listing over /repositories/ also returns every
// /registered_by, /project and /hooks/<id> row filed beside it, and
// stripping the prefix off those would read back as repositories that were
// never registered.
func listRepositoryKeys(ctx context.Context, kv Store) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, repositoriesPrefix, idRowPattern(repositoriesPrefix, repositoryKeyRowSuffix))
	if err != nil {
		return nil, err
	}
	return idsFromRows(rows, repositoriesPrefix, repositoryKeyRowSuffix)
}

// repositoryRecord is what the store holds for one registered repository.
type repositoryRecord struct {
	deployKey string
	project   string // empty until a Create links it
	owner     peer.ID
}

// fetchRepository reads a registered repository's row, or an error if key
// was never registered.
func fetchRepository(ctx context.Context, kv Store, key repokey.Key) (repositoryRecord, error) {
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
func repositoryOwner(ctx context.Context, kv Store, key repokey.Key) (owner peer.ID, registered bool) {
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
func registerRepository(ctx context.Context, kv Store, key repokey.Key, by peer.ID) error {
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
// hook filed under it — the /project link and every hook's own rows, not
// just its reverse-index pointer, so nothing survives under a key that no
// longer exists.
//
// The hook listing is the one place in this package that reads a
// sub-namespace of a variable-depth id, and it is where the takeover the
// file header describes happened. It is safe now for both reasons stated
// there: only this repository's rows are under this prefix at all, because
// segment(key) is one segment and cannot be the prefix of another
// repository's segment; and the pattern's `[^/]+` additionally refuses
// anything with a boundary left in it.
func deleteRepository(ctx context.Context, kv Store, key repokey.Key) error {
	prefix := repoHooksPrefix(key)
	hookRows, err := kv.ListRegEx(ctx, prefix, idRowPattern(prefix, ""))
	if err != nil {
		return fmt.Errorf("listing hooks: %w", err)
	}
	hookIDs, err := idsFromRows(hookRows, prefix, "")
	if err != nil {
		return err
	}

	batch, err := kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}

	for i, id := range hookIDs {
		if err := batchDeleteHook(batch, id); err != nil {
			return err
		}
		if err := batch.Delete(hookRows[i]); err != nil {
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
//	/projects/<id>/name     also the marker row
//	/projects/<id>/config   config repository's canonical key
//	/projects/<id>/code     code repository's canonical key

const projectsPrefix = "/projects/"

func projectRoot(id string) string      { return projectsPrefix + segment(id) }
func projectNameRow(id string) string   { return projectRoot(id) + projectNameRowSuffix }
func projectConfigRow(id string) string { return projectRoot(id) + "/config" }
func projectCodeRow(id string) string   { return projectRoot(id) + "/code" }

const projectNameRowSuffix = "/name"

// listProjectIDs mirrors listRepositoryKeys: the /name marker row, matched
// anchored, so a future per-project row (an owners list, say — v1 has one,
// /projects/<id>/owners/<...>) cannot make a project id appear twice or
// appear where none was ever registered.
func listProjectIDs(ctx context.Context, kv Store) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, projectsPrefix, idRowPattern(projectsPrefix, projectNameRowSuffix))
	if err != nil {
		return nil, err
	}
	return idsFromRows(rows, projectsPrefix, projectNameRowSuffix)
}

type projectRecord struct {
	name   string
	config string
	code   string
}

func fetchProject(ctx context.Context, kv Store, id string) (projectRecord, error) {
	name, err := kv.Get(ctx, projectNameRow(id))
	if err != nil || name == nil {
		return projectRecord{}, fmt.Errorf("project %q not found", id)
	}
	config, _ := kv.Get(ctx, projectConfigRow(id))
	code, _ := kv.Get(ctx, projectCodeRow(id))
	return projectRecord{name: string(name), config: string(config), code: string(code)}, nil
}

func putProject(ctx context.Context, kv Store, id, name string, config, code repokey.Key) error {
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
func linkRepositoryToProject(ctx context.Context, kv Store, key repokey.Key, projectID string) error {
	return kv.Put(ctx, repoProjectRow(key), []byte(projectID))
}

// ──────────────────────────────── hooks ────────────────────────────────
//
//	/hooks/<id>/repository   the one repository key it speaks for — also the marker row
//	/hooks/<id>/handle       the forge's own id for the webhook it created, opaque
//	/hooks/<id>/scheme       how the forge signs deliveries (pkg/forge.Scheme)
//	/hooks/<id>/secret       the wire secret the forge was given
//
// NO PROVIDER ROW. The host in the repository key is the forge, and a second
// row saying which could only agree with it or lie. The scheme is stored and
// never derived from the host: an instance upgraded to a stronger scheme keeps
// verifying the hooks it created under the old one.
//
// <id> is segment()-encoded like every other id here.

const hooksPrefix = "/hooks/"

func hookRoot(id string) string      { return hooksPrefix + segment(id) }
func hookRepoRow(id string) string   { return hookRoot(id) + hookMarkerSuffix }
func hookHandleRow(id string) string { return hookRoot(id) + "/handle" }
func hookSchemeRow(id string) string { return hookRoot(id) + "/scheme" }
func hookSecretRow(id string) string { return hookRoot(id) + "/secret" }

const hookMarkerSuffix = "/repository"

func listHookIDs(ctx context.Context, kv Store) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, hooksPrefix, idRowPattern(hooksPrefix, hookMarkerSuffix))
	if err != nil {
		return nil, err
	}
	return idsFromRows(rows, hooksPrefix, hookMarkerSuffix)
}

type hookRecord struct {
	repository string
	handle     string
	scheme     string
	secret     string
}

// errNoHook is a hook id nothing registered, which is the one failure a
// caller may be told as a NotFound; a registered hook missing a row is a store
// that broke, and is not.
var errNoHook = errors.New("no such hook")

func fetchHook(ctx context.Context, kv Store, id string) (hookRecord, error) {
	repository, err := kv.Get(ctx, hookRepoRow(id))
	switch {
	case errors.Is(err, datastore.ErrNotFound), err == nil && repository == nil:
		return hookRecord{}, fmt.Errorf("hook %q: %w", id, errNoHook)
	case err != nil:
		// A STORE THAT FAILED IS NOT AN UNKNOWN HOOK. Answered as NotFound it
		// would read, in a forge's delivery log, like a deleted hook.
		return hookRecord{}, fmt.Errorf("hook %q: %w", id, err)
	}

	record := hookRecord{repository: string(repository)}
	for row, into := range map[string]*string{
		hookHandleRow(id): &record.handle,
		hookSchemeRow(id): &record.scheme,
		hookSecretRow(id): &record.secret,
	} {
		value, err := kv.Get(ctx, row)
		if err != nil || value == nil {
			return hookRecord{}, fmt.Errorf("hook %q has no usable %s row: %v", id, row, err)
		}
		*into = string(value)
	}
	return record, nil
}

// putHook files a hook whole, in one batch, so no reader ever sees a hook
// with a repository and no secret.
func putHook(ctx context.Context, kv Store, id string, h hookRecord) error {
	batch, err := kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}
	for row, value := range map[string]string{
		hookRepoRow(id):   h.repository,
		hookHandleRow(id): h.handle,
		hookSchemeRow(id): h.scheme,
		hookSecretRow(id): h.secret,
	} {
		if err := batch.Put(row, []byte(value)); err != nil {
			return fmt.Errorf("batching hook %q: %w", id, err)
		}
	}
	return batch.Commit()
}

// batchDeleteHook queues every row a hook owns.
func batchDeleteHook(batch kvdb.Batch, id string) error {
	for _, row := range []string{hookRepoRow(id), hookHandleRow(id), hookSchemeRow(id), hookSecretRow(id)} {
		if err := batch.Delete(row); err != nil {
			return fmt.Errorf("batching hook delete: %w", err)
		}
	}
	return nil
}

// ───────────────────────────── certificates ─────────────────────────────
//
//	/acme/<fqdn>/certificate/pem      ACME-issued, autocert-managed
//	/static/<fqdn>/certificate/pem    operator-injected
//	/acme-cache/<key>/data            autocert's own opaque cache
//
// <fqdn> and <key> go through segment() like every other id. v1 spelled
// them with base64.StdEncoding (services/auth/api_acme.go), whose alphabet
// CONTAINS "/" — so a v1 certificate row's depth depends on the bytes of
// the fqdn. Nothing reads these three by prefix today, so that never became
// a bug there; it is not carried forward because "no listing happens to
// read this one" is a property of today's callers, not of the key.
//
// The autocert cache is its own top-level prefix rather than /acme/cache/,
// so that the question "can segment(fqdn) be the literal `cache`?" does not
// have to be answered at all.

func acmeCertRow(fqdn string) string   { return "/acme/" + segment(fqdn) + "/certificate/pem" }
func staticCertRow(fqdn string) string { return "/static/" + segment(fqdn) + "/certificate/pem" }
func cacheDataRow(key string) string   { return "/acme-cache/" + segment(key) + "/data" }

// wildcardFQDN replaces fqdn's leftmost label with "*", the same
// transformation services/auth/helpers.go's generateWildCardDomain makes:
// "www.example.com" -> "*.example.com". A single-label fqdn becomes "*".
func wildcardFQDN(fqdn string) string {
	split := strings.SplitAfterN(fqdn, ".", 2)
	split[0] = "*"
	return strings.Join(split, ".")
}
