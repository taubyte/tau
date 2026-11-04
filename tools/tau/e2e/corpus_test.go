//go:build dreaming && docker_integration

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/services/build/backend"

	"github.com/taubyte/tau/pkg/containers"

	"github.com/taubyte/tau/pkg/forge/drivers"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/invoke"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services"
	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// Every project under instance/dream/projects/ goes through both ways a project reaches
// a cloud: `tau build` (built here, handed to a universe) and `tau ci run` (the
// universe's CI builds the pushed commit). Each must deploy and serve, a refused
// project must be refused by name both ways, and both must build the same recipe.
// Sources are the fixture crates (and pkg/invoke/wasm/fixtures/languages,
// pkg/invoke/js/fixtures), placed under services/ with the world at wit/.

// crateOf is the fixture crate a source path is: by its own name, unless named here.
var crateOf = map[string]string{"hello": "handler", "keeper": "cacher", "reader": "cacher"}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	return root
}

var sourceLine = regexp.MustCompile(`(?m)^\s*source: (services/[a-z_]+)\s*$`)

// assemble is a corpus project as the repository it is pushed as.
func assemble(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	from := filepath.Join(root, "instance", "dream", "projects", filepath.FromSlash(name))
	require.NoError(t, copyTree(from, dir))

	sources := map[string]bool{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".yaml" {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range sourceLine.FindAllStringSubmatch(string(body), -1) {
			sources[m[1]] = true
		}
		return nil
	}))
	fixtures := filepath.Join(root, "pkg", "invoke", "wasm", "fixtures")
	for source := range sources {
		base := filepath.Base(source)
		crate := base
		if named, ok := crateOf[base]; ok {
			crate = named
		}
		if script := filepath.Join(root, "pkg", "invoke", "js", "fixtures", base); dirExists(script) {
			require.NoError(t, copyTree(script, filepath.Join(dir, source)))
			continue
		}
		// A SOURCE IN ANOTHER LANGUAGE is its whole directory under
		// fixtures/languages, as with/languages names it.
		if lang := filepath.Join(fixtures, "languages", base); dirExists(lang) {
			require.NoError(t, copyTree(lang, filepath.Join(dir, source)))
			continue
		}
		// A SOURCE NOTHING BUILDS has no crate: an application's source is
		// a path its author never fills when it names no build.
		if _, err := os.Stat(filepath.Join(fixtures, crate)); os.IsNotExist(err) {
			continue
		}
		for _, file := range []string{"Cargo.toml", "Cargo.lock", "src/lib.rs"} {
			require.NoError(t, copyFile(filepath.Join(fixtures, crate, file), filepath.Join(dir, source, file)))
		}
	}
	if len(sources) > 0 {
		require.NoError(t, copyTree(filepath.Join(root, "pkg", "invoke", "wasm", "wit"), filepath.Join(dir, "wit")))
	}
	return dir
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		return copyFile(p, filepath.Join(to, rel))
	})
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}

// universe is a development cloud that builds: one node running every private
// service with a container daemon to build and run in, and one running the
// public build frontend. It answers the frontend's address and the node that
// serves.
func universe(t *testing.T, f *fake.Served, configure func(*instance.Config), opts ...instance.Option) (string, *instance.Instance) {
	t.Helper()
	var err error
	forges, err = f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	t.Cleanup(func() { forges = nil })
	hash, err := auth.HashAdminPassword(adminPassword)
	require.NoError(t, err)
	adminSecret, err := secrets.Sealed(secrets.Ref{Scope: secrets.Cloud(auth.VaultService), Name: auth.AdminSecret}, []byte(hash), time.Now())
	require.NoError(t, err)

	u, err := dream.Start(t.Context(), strings.ReplaceAll(t.Name(), "/", "-"), dream.Config{
		Copies:    2,
		Container: containers.Docker,
		Options:   append([]instance.Option{instance.WithForges(forges)}, opts...),
		Secrets:   []*secretsv2.InitSecret{adminSecret},
		Configure: func(index int, c *instance.Config) {
			c.CloudFqdn = e2eCloud
			c.Forges = []drivers.Forge{{Host: f.Host(), Driver: drivers.GitHub, HostKeys: []string{f.HostKey()}}}
			c.Auth.Admin = auth.Admin{Name: "root"}
			if configure != nil {
				configure(c)
			}
			if index > 0 {
				c.Services = []string{instance.Discovery, instance.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	serving, ok := dream.Node(u.Pids()[0])
	require.True(t, ok)
	front, ok := dream.Node(u.Pids()[1])
	require.True(t, ok)
	addr := front.BuildFrontendAddr()
	require.NotNil(t, addr)
	return "http://" + addr.String(), serving
}

// probe is something a deployed project must answer, and what it answers: a
// request through the gateway when a host is named, an invocation of the named
// function on the worker otherwise, or a DNS question.
type probe struct {
	host     string
	function string
	// kind is the invoked resource's kind; zero is a function.
	kind string
	dns  string

	method, path, body string
	status             int
	answer             string
	contains           string
	// budget is how long the cloud may take to start answering; zero is two
	// minutes.
	budget time.Duration
}

// corpusCase is one project: what proves it deployed, or why it is refused.
type corpusCase struct {
	name   string
	probes []probe
	// refused is the reason both ways must name.
	refused string
	// prepare runs once the project is linked, before either build.
	prepare func(t *testing.T, work string)
	// region is the one region the universe is, for a project that says where
	// it runs; empty is a cloud of one unnamed region.
	region string
	// from writes the project somewhere other than instance/dream/projects and
	// answers where; nil assembles the corpus project the name names.
	from func(t *testing.T) string
}

func (c corpusCase) repo() string {
	return strings.NewReplacer("/", "-", "_", "-").Replace(c.name)
}

// codeRepo is a repository a corpus project's resource is built from, as
// `source.git` names it: the crate at the source's path, and the world.
type codeRepo struct {
	key  repokey.Key
	dir  string
	path string
}

var gitSource = regexp.MustCompile(`git: (https://github\.com/acme/([a-z_-]+))\n(?:\s+branch: [a-z]+\n)?\s+path: (\S+)`)

// codeRepos rewrites each `source.git` in the assembled project to the forge
// the universe serves, and assembles the repository it names.
func codeRepos(t *testing.T, work string, f *fake.Served) []codeRepo {
	t.Helper()
	var out []codeRepo
	require.NoError(t, filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".yaml" {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range gitSource.FindAllStringSubmatch(string(body), -1) {
			key := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: m[2]}
			s, err := forges.For(key, f.Token())
			if err != nil {
				return err
			}
			addr, err := s.CloneAddress(key, forge.HTTPS)
			if err != nil {
				return err
			}
			body = []byte(strings.ReplaceAll(string(body), m[1], addr.URL))

			dir := t.TempDir()
			crate := filepath.Base(m[3])
			if named, ok := crateOf[crate]; ok {
				crate = named
			}
			root := repoRoot(t)
			for _, file := range []string{"Cargo.toml", "Cargo.lock", "src/lib.rs"} {
				if err := copyFile(filepath.Join(root, "pkg", "invoke", "wasm", "fixtures", crate, file), filepath.Join(dir, m[3], file)); err != nil {
					return err
				}
			}
			if err := copyTree(filepath.Join(root, "pkg", "invoke", "wasm", "wit"), filepath.Join(dir, "wit")); err != nil {
				return err
			}
			out = append(out, codeRepo{key: key, dir: dir, path: m[3]})
		}
		return os.WriteFile(p, body, 0o644)
	}))
	return out
}

// pushed commits a code repository and pushes it to the forge, as its author
// would; the config repository is pushed by `tau link --push`.
func pushed(t *testing.T, f *fake.Served, r codeRepo) {
	t.Helper()
	s, err := forges.For(r.key, f.Token())
	require.NoError(t, err)
	addr, err := s.CloneAddress(r.key, forge.HTTPS)
	require.NoError(t, err)
	auth := "Authorization: Basic " + basic(addr.HTTPUsername, f.TokenString())
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "code"}, {"-c", "http.extraHeader=" + auth, "push", "-q", addr.URL, "HEAD:refs/heads/main"}} {
		gitOK(t, r.dir, args...)
	}
}

func TestEveryCorpusProjectBuildsBothWays(t *testing.T) {
	for _, c := range corpus {
		name := c.name
		if c.region != "" {
			name += "@" + c.region
		}
		t.Run(name, func(t *testing.T) { bothWays(t, c) })
	}
}

var corpus = []corpusCase{
	{name: "with/function/wasm", probes: []probe{
		{host: "hello.example.com", method: http.MethodGet, path: "/", status: 200},
		{host: "hello.example.com", method: http.MethodPost, path: "/orders", status: 201},
	}},
	// THE JS TIER answers with the method and the body, from a bundle the
	// author's build made and the platform packed.
	{name: "with/function/js", probes: []probe{
		{host: "js.example.com", method: http.MethodPost, path: "/", body: "ping", status: 200, answer: "POST ping"},
		{host: "js.example.com", method: http.MethodGet, path: "/orders", status: 200, answer: "GET "},
	}},
	// A BUILD READS A SECRET through the helper, set the way a developer sets one: both
	// builds bake in the fingerprint of the value set, and neither has it in its
	// environment. A function of the same package reads it at the call, through the
	// vault module.
	{name: "with/package/vault", prepare: func(t *testing.T, work string) {
		t.Setenv("TAU_SECRET_SIGNING_KEY", signingKey)
		ok(t, "secrets", "set", "signing_key", signingKey, "--dir", work, "--env", "prod")
	}, probes: []probe{
		{host: "vault.example.com", method: http.MethodGet, path: "/", status: 200, answer: fingerprintOf(signingKey)},
		{host: "reader.example.com", method: http.MethodGet, path: "/", status: 200, answer: fingerprintOf(signingKey)},
	}},
	// TWO WEBSITES ON ONE NAME, each compiled into the gateway that serves its package's
	// files: the static one answers a path it lacks with its own page and 404, the
	// single-page app at /docs answers every route under it with its one page.
	{name: "with/website", probes: []probe{
		{host: "www.example.com", method: http.MethodGet, path: "/", status: 200, answer: "<h1>home</h1>"},
		{host: "www.example.com", method: http.MethodGet, path: "/nope", status: 404, answer: "<h1>gone</h1>"},
		{host: "www.example.com", method: http.MethodGet, path: "/docs/", status: 200, answer: "<div id=docs></div>"},
		{host: "www.example.com", method: http.MethodGet, path: "/docs/guide/intro", status: 200, answer: "<div id=docs></div>"},
		{host: "www.example.com", method: http.MethodPost, path: "/", status: 405},
	}},
	// ONE HOST, A FUNCTION ON EACH TIER: the componentized one says it is js
	// compiled to wasm, and the packed one answers on the js tier.
	{name: "with/function/runtimes", probes: []probe{
		{host: "both.example.com", method: http.MethodPost, path: "/js", body: "ping", status: 200, answer: "POST ping"},
		{host: "both.example.com", method: http.MethodPost, path: "/wasm", body: "ping", status: 200, answer: "js POST /wasm ping"},
	}},
	{name: "with/languages", probes: languageProbes},
	// THE STARTER is what `tau init` writes, and nothing but that: it builds
	// from the source init ships and answers.
	{name: "starter", from: initialised, probes: []probe{
		{function: "hello", method: http.MethodGet, path: "/orders", status: 200, answer: "hello from /orders\n"},
	}},
	{name: "with/component", probes: []probe{
		{host: "shared.example.com", method: http.MethodGet, path: "/", status: 200},
	}},
	{name: "with/bus", probes: []probe{
		{host: "live.example.com", method: http.MethodPost, path: "/broadcast/live/", body: "deploy finished", status: 204},
	}},
	{name: "with/cache/authorizer", probes: []probe{
		{function: "reacher", method: http.MethodPost, path: "/put/sessions/a", body: "alice", status: 204},
		{function: "reacher", method: http.MethodPost, path: "/get/sessions/a", status: 200, answer: "alice"},
	}},
	{name: "with/cache/crdt", probes: []probe{
		{function: "cacher", method: http.MethodPost, path: "/roundtrip/sessions/user:1", body: "alice", status: 200, answer: "alice"},
	}},
	{name: "with/channel/function", probes: []probe{
		{function: "dialer", method: http.MethodPost, path: "/dial/reserve", body: "hello", status: 200},
	}},
	{name: "with/channel/application", probes: []probe{
		{function: "dialer", method: http.MethodPost, path: "/dial/reserve", body: "GET /tau-corpus HTTP/1.0\r\nHost: whoami\r\n\r\n", status: 200, contains: "GET /tau-corpus ", budget: 3 * time.Minute},
	}},
	{name: "with/application", probes: []probe{
		{host: "whoami.example.com", method: http.MethodGet, path: "/tau-corpus", status: 200, contains: "GET /tau-corpus", budget: 3 * time.Minute},
	}},
	{name: "with/worker/container", probes: []probe{
		{kind: "worker", function: "exporter", method: http.MethodPost, path: "/", body: "echo rows=$((40+2))", status: 200, contains: "rows=42", budget: 3 * time.Minute},
	}},
	{name: "with/cron/queue"},
	{name: "with/cron/skip"},
	{name: "with/database/distributed/kv/crdt", probes: []probe{
		{host: "keep.example.com", method: http.MethodPost, path: "/kv-put/sessions/token-1", body: "body", status: 204},
		{host: "keep.example.com", method: http.MethodGet, path: "/kv-get/sessions/token-1", status: 200, answer: "body"},
	}},
	{name: "with/database/distributed/kv/crdt-b", probes: []probe{
		{host: "keepb.example.com", method: http.MethodPost, path: "/kv-put/sessions/token-1", body: "body", status: 204},
	}},
	{name: "with/database/distributed/kv/raft", probes: []probe{
		{host: "ledger.example.com", method: http.MethodPost, path: "/kv-put/ledger/entry-1", body: "body", status: 204, budget: 3 * time.Minute},
	}},
	{name: "with/database/distributed/kv/raft-b", probes: []probe{
		{host: "ledgerb.example.com", method: http.MethodPost, path: "/kv-put/ledger/entry-1", body: "body", status: 204, budget: 3 * time.Minute},
	}},
	{name: "with/database/distributed/sql/crdt", probes: []probe{
		{function: "keeper", method: http.MethodPost, path: "/query/ledger", body: "SELECT 1", status: 200},
	}},
	// An external database needs a server this universe lacks
	// (test-external-databases serves it); here it deploys, builds and resolves.
	{name: "with/database/mysql/sql"},
	{name: "with/database/postgres/sql"},
	{name: "with/dns", probes: []probe{
		{dns: "geo.example.com", status: 200, contains: "203.0.113.9"},
	}},
	{name: "with/gateway/router", probes: []probe{
		{host: "routed.example.com", method: http.MethodPost, path: "/whoami", body: "body", status: 200, contains: "/prod/function/saw_whoami"},
		{host: "routed.example.com", method: http.MethodPost, path: "/answer", status: http.StatusFound},
	}},
	{name: "with/storage/object", probes: []probe{
		{function: "cacher", method: http.MethodPost, path: "/obj-put/receipts/2026/01", body: "a receipt", status: 200},
	}},
	{name: "with/stream/distributed", probes: []probe{
		{function: "cacher", method: http.MethodPost, path: "/publish/orders/", body: "order:1", status: 204},
	}},
	{name: "with/sync/log", probes: []probe{
		{function: "cacher", method: http.MethodPost, path: "/append/audit/", body: "one", status: 200, budget: 3 * time.Minute},
	}},
	{name: "with/sync/queue", probes: []probe{
		{function: "cacher", method: http.MethodPost, path: "/send/jobs/order-1", body: "ship it", status: 204, budget: 3 * time.Minute},
	}},
	{name: "with/views", region: "region-1", probes: []probe{
		{host: "regional.example.com", method: http.MethodGet, path: "/", status: 200},
	}},
	{name: "with/placement", region: "region-1"},
	// A CLOUD OF ONE UNNAMED REGION serves no project that names its regions,
	// and says which.
	{name: "with/placement", refused: "which this build does not serve"},
	{name: "refused/application/noartifact", refused: "is of type oci and names no image and no build"},
	{name: "refused/environments/missing", refused: "declares no environments"},
	{name: "refused/function/noruntime", refused: "required attribute 'type'"},
	{name: "refused/function/unknownruntime", refused: "valid values here: wasm, warp, oci, assets"},
	{name: "refused/views/across", region: "region-2", refused: `reaches cache/sessions_r1, which is not placed in "region-2" where this is`},
	{name: "refused/website/notfiles", refused: "serves package module, which is of type wasm"},
}

// signingKey is with/package/vault's secret, and fingerprintOf what its build bakes in.
const signingKey = "sk_build_1"

func fingerprintOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

// languageProbes asks every function of with/languages, each built by the
// published image for its language, and expects its language's name back with
// what it was sent.
var languageProbes = func() []probe {
	var out []probe
	for _, l := range [][2]string{
		{"rust", "rust"}, {"c", "c"}, {"cpp", "c++"}, {"python", "python"}, {"js", "js"},
		{"typescript", "typescript"}, {"csharp", "csharp"}, {"tinygo", "tinygo"}, {"golang", "go"},
	} {
		out = append(out, probe{function: l[0], method: http.MethodPost, path: "/echo", body: "ping", status: 200, answer: l[1] + " POST /echo ping"})
	}
	return out
}()

// initialised is a project as `tau init` writes it.
func initialised(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	r := tau("init", "starter", "--dir", dir)
	require.Equal(t, 0, r.code, "tau init\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	return dir
}

type built struct {
	Built  []builtResource `json:"built"`
	Job    string          `json:"job"`
	Status string          `json:"status"`
}

type builtResource struct {
	Environment, Kind, Name, ID, Recipe string
}

// underTest is a universe a developer is signed in to, at a forge it serves.
type underTest struct {
	f       *fake.Served
	serving *instance.Instance
}

// signedIn starts a forge and a universe and signs in to it.
func signedIn(t *testing.T, configure func(*instance.Config), opts ...instance.Option) *underTest {
	t.Helper()
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	gitEnv(t)

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	cloud, serving := universe(t, f, configure, opts...)

	var login result
	require.Eventually(t, func() bool {
		login = tauIn(f.TokenString()+"\n", "login", "--cloud", cloud, "--sso", f.Host(), "--paste")
		return login.code == 0
	}, 2*time.Minute, time.Second, "login never succeeded: %s", login.stderr)
	return &underTest{f: f, serving: serving}
}

func bothWays(t *testing.T, c corpusCase) {
	if c.region == "" {
		signedIn(t, nil).bothWays(t, c)
		return
	}
	signedIn(t, func(cfg *instance.Config) { cfg.Region = c.region }).bothWays(t, c)
}

func (u *underTest) bothWays(t *testing.T, c corpusCase) {
	f, serving := u.f, u.serving

	var work string
	if c.from != nil {
		work = c.from(t)
	} else {
		work = assemble(t, c.name)
	}
	code := codeRepos(t, work, f)
	configRepo := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: c.repo()}
	args := []string{"link", "--create", "--push", "--dir", work, "--config-repo", configRepo.String(), "-o", "json"}
	for _, r := range code {
		args = append(args, "--code-repo", r.key.String())
	}
	var link result
	require.Eventually(t, func() bool {
		link = tau(args...)
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)
	for _, r := range code {
		pushed(t, f, r)
	}
	id := projectYAMLIDIn(t, work)
	if c.prepare != nil {
		c.prepare(t, work)
	}

	local := tau("build", work, "-o", "json")
	if c.refused != "" {
		require.NotZero(t, local.code, "tau build of a refused project")
		// REFUSED HERE OR BY THE UNIVERSE: what only a cloud can judge — its
		// regions, a zone's proof — is the deploy's refusal, in its log.
		said := local.stdout + local.stderr
		var b built
		if json.Unmarshal([]byte(local.stdout), &b) == nil && b.Job != "" {
			require.Equal(t, "failed", b.Status)
			said += logOf(t, b.Job)
		}
		require.Contains(t, said, c.refused)
	} else {
		require.Equal(t, 0, local.code, "tau build\nstdout: %s\nstderr: %s", local.stdout, local.stderr)
	}
	var uploads []built
	if c.refused == "" {
		var b built
		require.NoError(t, json.Unmarshal([]byte(local.stdout), &b))
		require.Equal(t, "success", b.Status, "the universe deployed the upload")
		uploads = append(uploads, b)
		for _, r := range code {
			res := tau("build", r.dir, "--project", id, "--repo", r.key.String(), "-o", "json")
			require.Equal(t, 0, res.code, "tau build of %s\nstdout: %s\nstderr: %s", r.key, res.stdout, res.stderr)
			var b built
			require.NoError(t, json.Unmarshal([]byte(res.stdout), &b))
			require.Equal(t, "success", b.Status)
			require.NotEmpty(t, b.Built, "%s builds something", r.key)
			uploads = append(uploads, b)
		}
		for _, p := range c.probes {
			answered(t, serving, id, p)
		}
	}

	ci := tau("ci", "run", "--dir", work, "-o", "json")
	type ranJob struct{ Job, Status string }
	var ran ranJob
	if c.refused != "" {
		require.NotZero(t, ci.code)
		require.NoError(t, json.Unmarshal([]byte(ci.stdout), &ran), ci.stderr)
		require.Equal(t, "failed", ran.Status)
		require.Contains(t, logOf(t, ran.Job), c.refused)
		envs, err := serving.Register().Local().Environments(t.Context(), id)
		require.NoError(t, err)
		require.Empty(t, envs, "nothing was published for a refused project")
		return
	}
	require.Equal(t, 0, ci.code, "tau ci run\nstdout: %s\nstderr: %s", ci.stdout, ci.stderr)
	require.NoError(t, json.Unmarshal([]byte(ci.stdout), &ran))
	require.Equal(t, "success", ran.Status)
	jobs := []string{ran.Job}
	for _, r := range code {
		res := tau("ci", "run", "--dir", r.dir, "--project", id, "--repo", r.key.String(), "-o", "json")
		require.Equal(t, 0, res.code, "tau ci run of %s\nstdout: %s\nstderr: %s", r.key, res.stdout, res.stderr)
		type ranJob struct{ Job, Status string }
		var ran ranJob
		require.NoError(t, json.Unmarshal([]byte(res.stdout), &ran))
		require.Equal(t, "success", ran.Status)
		jobs = append(jobs, ran.Job)
	}
	for _, p := range c.probes {
		answered(t, serving, id, p)
	}

	// Local and CI build the same recipes for the same resources; the bytes are the
	// toolchain's business.
	local1, cloned := map[string]string{}, map[string]string{}
	recipes := 0
	for i, b := range uploads {
		for k, v := range assetsOf(t, b.Job) {
			local1[k] = v
		}
		log := logOf(t, jobs[i])
		for _, x := range b.Built {
			recipes++
			require.Contains(t, log, fmt.Sprintf("building %s/%s for %s (recipe %s)", x.Kind, x.Name, x.Environment, x.Recipe),
				"the cloud planned the recipe this machine built")
		}
		for k, v := range assetsOf(t, jobs[i]) {
			cloned[k] = v
		}
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(local1)), slices.Collect(maps.Keys(cloned)),
		"the build on this machine and the cloud's built the same resources")
	require.Len(t, cloned, recipes, "one artifact per recipe")

	// The deployment resolves every resource it built to what was built.
	compiled, err := serving.Register().Local().Resolve(t.Context(), id, "prod")
	require.NoError(t, err)
	object, err := serving.Register().Local().Object(t.Context(), compiled.Object)
	require.NoError(t, err)
	p, err := specs.Load(object)
	require.NoError(t, err)
	for _, b := range p.Buildables() {
		cid, err := serving.BuildArtifacts().Artifact(t.Context(), id, "prod", specs.KindPackage, b.ID)
		require.NoError(t, err, "package/%s", b.Name)
		require.Equal(t, cloned["prod/"+b.ID], cid)
	}
}

// assetsOf is a job's artifacts, by the key its record files them under.
func assetsOf(t *testing.T, job string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, view := range builtBy(t, job) {
		maps.Copy(out, view.Built)
	}
	return out
}

// jobView is what `tau job -o json` says of a job.
type jobView struct {
	Built       map[string]string `json:"asset_cid"`
	Environment string            `json:"environment"`
	Children    []string          `json:"children"`
	Reason      string            `json:"reason"`
	ID          string            `json:"id"`
}

func viewOf(t *testing.T, job string) jobView {
	t.Helper()
	var view jobView
	require.NoError(t, json.Unmarshal([]byte(ok(t, "job", "-o", "json", "--", job)), &view))
	return view
}

// builtBy is the environment jobs a push's job stands for: the plan's children, or
// the job itself when it builds an environment.
func builtBy(t *testing.T, job string) []jobView {
	t.Helper()
	view := viewOf(t, job)
	if view.Environment != "" {
		return []jobView{view}
	}
	out := make([]jobView, 0, len(view.Children))
	for _, child := range view.Children {
		out = append(out, viewOf(t, child))
	}
	return out
}

// logOf is what a push's job said: its plan's reason when it failed, and each
// environment job's log.
func logOf(t *testing.T, job string) string {
	t.Helper()
	said := viewOf(t, job).Reason
	for _, view := range builtBy(t, job) {
		said += ok(t, "logs", "--", view.ID)
	}
	return said
}

// answered asks the serving node what the internet would, until it answers.
func answered(t *testing.T, n *instance.Instance, project string, p probe) {
	t.Helper()
	budget := p.budget
	if budget == 0 {
		budget = 2 * time.Minute
	}
	var last string
	require.Eventually(t, func() bool {
		status, body, err := ask(t, n, project, p)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", status, body)
		return status == p.status && (p.answer == "" || body == p.answer) && strings.Contains(body, p.contains)
	}, budget, time.Second, "%+v never answered: %s", p, said{&last})
}

func ask(t *testing.T, n *instance.Instance, project string, p probe) (int, string, error) {
	switch {
	case p.host != "":
		req := httptest.NewRequest(p.method, "http://"+p.host+p.path, strings.NewReader(p.body))
		req.Host = p.host
		w := httptest.NewRecorder()
		n.Handler().ServeHTTP(w, req)
		return w.Code, w.Body.String(), nil
	case p.dns != "":
		return lookup(t, n, p.dns)
	}
	kind := p.kind
	if kind == "" {
		kind = "function"
	}
	res, err := n.Worker().Invoke(t.Context(), specs.Address{Project: project, Environment: "prod", Kind: kind, Name: p.function},
		&invoke.Request{Method: p.method, Path: p.path, Body: []byte(p.body)})
	if err != nil {
		return 0, "", err
	}
	return res.Status, string(res.Body), nil
}

// lookup asks the node's own DNS server for name's A record over UDP, the one
// interface a resolver has, and answers 200 and the address when it has one.
func lookup(t *testing.T, n *instance.Instance, name string) (int, string, error) {
	var at string
	for _, a := range n.DNSAddrs() {
		if _, ok := a.(*net.UDPAddr); ok {
			at = a.String()
		}
	}
	if at == "" {
		return 0, "", fmt.Errorf("the node binds no udp dns socket")
	}
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", at)
	}}
	addrs, err := r.LookupHost(t.Context(), name)
	if err != nil {
		return 0, "", err
	}
	return 200, strings.Join(addrs, ","), nil
}

// said renders what the last attempt answered when the assertion gives up.
type said struct{ at *string }

func (s said) String() string { return *s.at }

// txtZone is the public DNS a domain claim is proven against.
type txtZone struct {
	mu sync.Mutex
	m  map[string][]string
}

func (z *txtZone) set(name, value string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.m[name] = []string{value}
}

func (z *txtZone) LookupTXT(_ context.Context, name string) ([]string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if v, ok := z.m[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}

func TestTheDomainCorpusBuildsBothWays(t *testing.T) {
	dns := &txtZone{m: map[string][]string{}}
	u := signedIn(t, func(c *instance.Config) {
		built := c.Sections[instance.BuildBackend].(backend.Config)
		built.Domains.Trusted = false
		c.Sections[instance.BuildBackend] = built
		c.Services = append(append(append([]string(nil), instance.All...), services.Names()...), instance.CM)
	}, instance.WithResolver(dns))

	claim := func(t *testing.T, work, fqdn string) api.DomainClaim {
		t.Helper()
		var c api.DomainClaim
		require.Eventually(t, func() bool {
			r := tau("domain", "claim", fqdn, "--dir", work, "-o", "json")
			return r.code == 0 && json.Unmarshal([]byte(r.stdout), &c) == nil
		}, 2*time.Minute, time.Second, "a cm node signs a claim once the vault answers")
		return c
	}

	var owner api.DomainClaim
	for _, c := range []corpusCase{
		{name: "with/domain/apex", prepare: func(t *testing.T, work string) {
			owner = claim(t, work, "shop.test")
			dns.set(owner.Entry, owner.Token)
		}, probes: []probe{{host: "www.shop.test", method: http.MethodGet, path: "/", status: 200}}},
		{name: "refused/domain/stolen", refused: "tau domain claim shop.test", prepare: func(t *testing.T, work string) {
			dns.set(projectYAMLIDIn(t, work)[:8]+".shop.test", owner.Token)
		}},
		{name: "with/domain/child", probes: []probe{{host: "www.eu.shop.test", method: http.MethodGet, path: "/", status: 200}}},
		{name: "refused/domain/stolenchild", refused: "shop.test is proven to be another project's", prepare: func(t *testing.T, work string) {
			stolen := claim(t, work, "eu.shop.test")
			dns.set(stolen.Entry, stolen.Token)
		}},
	} {
		t.Run(c.name, func(t *testing.T) { u.bothWays(t, c) })
	}
}
