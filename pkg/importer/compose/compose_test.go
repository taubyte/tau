package compose

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func importOf(t *testing.T, yml string) importer.Result {
	t.Helper()
	r, err := Import([]byte(yml))
	require.NoError(t, err)
	return r
}

// finding returns the finding for a service+key, and says so if there is none —
// "it reported nothing" is the failure this package exists to prevent.
func finding(t *testing.T, r importer.Result, service, key string) importer.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Resource == service && f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q/%q; got %v", service, key, codes(r))
	return importer.Finding{}
}

func codes(r importer.Result) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Resource+"/"+f.Key)
	}
	return out
}

func hasFinding(r importer.Result, service, key string) bool {
	for _, f := range r.Findings {
		if f.Resource == service && f.Key == key {
			return true
		}
	}
	return false
}

// TestTheTargetKindIsVerifiedAgainstTheVocabulary. If the kind is renamed or
// stops running containers, the import fails loudly instead of emitting a
// document for a kind that cannot run it.
func TestTheTargetKindIsVerifiedAgainstTheVocabulary(t *testing.T) {
	kind, err := targetKind()
	require.NoError(t, err)
	require.Contains(t, v2.EnginesOf(kind), "container",
		"the importer's target must be a kind the vocabulary says runs containers")
}

// TestWhatCarriesAcrossFaithfully — the accept rows of the fidelity matrix.
func TestWhatCarriesAcrossFaithfully(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: acme/api:1.2.3
    command: ["serve", "--port", "8080"]
    working_dir: /srv
    environment:
      DATABASE_URL: postgres://db/app
      LOG_LEVEL: debug
`)
	require.Equal(t, "application", r.Kind)
	api := r.Resources["api"]
	require.Equal(t, "acme/api:1.2.3", api["image"])
	require.Equal(t, []string{"serve", "--port", "8080"}, api["command"])
	require.Equal(t, "/srv", api["workdir"])
	require.Equal(t, map[string]any{
		"DATABASE_URL": "postgres://db/app",
		"LOG_LEVEL":    "debug",
	}, api["env"])

	require.Empty(t, r.Blocked(), "nothing here needs a vocabulary tau lacks")
}

// TestEnvironmentIsReadInBothSpellings. Compose accepts a mapping and a
// KEY=VALUE list, and an importer that handled one would silently drop the
// other's variables.
func TestEnvironmentIsReadInBothSpellings(t *testing.T) {
	list := importOf(t, `
services:
  api:
    image: x
    environment:
      - A=1
      - B=two
`)
	require.Equal(t, map[string]any{"A": "1", "B": "two"}, list.Resources["api"]["env"])

	mapping := importOf(t, `
services:
  api:
    image: x
    environment:
      A: 1
      B: two
`)
	require.Equal(t, map[string]any{"A": "1", "B": "two"}, mapping.Resources["api"]["env"])
}

// TestAValuelessVariableIsNotInvented. `KEY:` with no value means "take it from
// my shell", and that value is not in this file — emitting an empty string
// would be inventing configuration.
func TestAValuelessVariableIsNotInvented(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    environment:
      FROM_SHELL:
      REAL: value
`)
	require.Equal(t, map[string]any{"REAL": "value"}, r.Resources["api"]["env"])
}

// TestABuildBecomesTheDockerfileBranchOfTheBuildStanza.
func TestABuildBecomesTheDockerfileBranchOfTheBuildStanza(t *testing.T) {
	r := importOf(t, `
services:
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.prod
      target: release
`)
	require.Equal(t, map[string]any{
		"dockerfile": map[string]any{
			"context": "./api",
			"path":    "Dockerfile.prod",
			"target":  "release",
		},
	}, r.Resources["api"]["build"])

	short := importOf(t, `
services:
  api:
    build: ./api
`)
	require.Equal(t, map[string]any{"dockerfile": map[string]any{"context": "./api"}},
		short.Resources["api"]["build"])
}

// TestAServiceWithNoSourceIsRefused rather than imported as an empty resource.
func TestAServiceWithNoSourceIsRefused(t *testing.T) {
	r := importOf(t, `
services:
  api:
    environment:
      A: 1
`)
	require.NotContains(t, r.Resources, "api", "there is nothing to run")
	f := finding(t, r, "api", "image")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "NO_SOURCE", f.Code)
}

// TestEveryBlockedFieldNamesTheContractItNeeds. importers.md's second rule:
// NO_TAU_FEATURE on its own is a shrug; naming the field makes it a to-do item,
// and counting those across a corpus prioritises the vocabulary by evidence.
func TestEveryBlockedFieldNamesTheContractItNeeds(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost/health"]
      interval: 30s
    restart: always
    user: "1000:1000"
    read_only: true
    init: true
    stop_grace_period: 30s
    volumes:
      - ./data:/var/lib/data
    env_file: .env
    entrypoint: /entry.sh
    depends_on:
      db:
        condition: service_healthy
`)
	for _, key := range []string{
		"healthcheck", "restart", "user", "read_only", "init",
		"stop_grace_period", "volumes", "env_file", "entrypoint", "depends_on",
	} {
		f := finding(t, r, "api", key)
		require.Equal(t, importer.Blocked, f.Level, key)
		require.NotEmpty(t, f.Needs,
			"%s is blocked but names no contract that would unblock it, which makes it a shrug rather than a to-do", key)
		require.NotEmpty(t, f.Detail, key)
	}
}

// TestNothingBlockedReachesTheEmittedConfig. importers.md's first rule: a
// config carrying restart: always into a system with no restart controller is
// worse than a refusal, because the next reader believes it.
func TestNothingBlockedReachesTheEmittedConfig(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    restart: always
    healthcheck:
      test: ["CMD", "true"]
    privileged: true
    cap_add: [SYS_ADMIN]
    devices: ["/dev/fuse"]
    network_mode: host
    deploy:
      replicas: 5
    volumes:
      - data:/var/lib/data
`)
	api := r.Resources["api"]
	for _, key := range []string{
		"restart", "healthcheck", "privileged", "cap_add", "devices",
		"network_mode", "deploy", "replicas", "volumes",
	} {
		require.NotContains(t, api, key,
			"%s reached the config; the runtime cannot honour it and the next reader would believe it", key)
	}
	require.Equal(t, "x", api["image"], "and what CAN be honoured is still there")
}

// TestPrivilegeEscalationIsRefusedOnPurpose, not merely unsupported — so the
// finding says so rather than promising it later.
func TestPrivilegeEscalationIsRefusedOnPurpose(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    privileged: true
    cap_add: [SYS_ADMIN]
    devices: ["/dev/fuse"]
`)
	for _, key := range []string{"privileged", "cap_add", "devices"} {
		f := finding(t, r, "api", key)
		require.Equal(t, importer.Blocked, f.Level, key)
		require.Empty(t, f.Needs,
			"%s must name no contract: it is a refusal, not a gap, and naming a field would promise it", key)
		require.Contains(t, f.Detail, "refused on purpose", key)
	}
}

// TestPublishedPortsAreApproximatedLoudly. tau routes to a workload by name;
// a host port mapping has no equivalent, and dropping it silently is how
// somebody's published service becomes unreachable without a word.
func TestPublishedPortsAreApproximatedLoudly(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    ports:
      - "8080:80"
      - "443:8443"
`)
	f := finding(t, r, "api", "ports")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "8080:80")
	require.NotContains(t, r.Resources["api"], "ports")
}

// TestAStringCommandIsNotAShell. Compose splits the string form with
// shell-like rules but does not run a shell, so a pipe or a $VAR behaves
// differently — and only says so when the content suggests one.
func TestAStringCommandIsNotAShell(t *testing.T) {
	shelly := importOf(t, `
services:
  api:
    image: x
    command: "serve | tee /var/log/out"
`)
	f := finding(t, shelly, "api", "command")
	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "COMMAND_STRING_IS_NOT_A_SHELL", f.Code)

	plain := importOf(t, `
services:
  api:
    image: x
    command: "serve --port 8080"
`)
	require.False(t, hasFinding(plain, "api", "command"),
		"a plain command needs no warning; crying wolf on every command teaches people to skip the findings")
	require.Equal(t, []string{"serve", "--port", "8080"}, plain.Resources["api"]["command"])
}

// TestDependsOnConditionsAreNamed, because service_healthy and
// service_completed_successfully are readiness and one-shot dependencies rather
// than ordering, and reading them as ordering is the classic import bug.
func TestDependsOnConditionsAreNamed(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    depends_on:
      db:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
`)
	f := finding(t, r, "api", "depends_on")
	require.Contains(t, f.Detail, "service_healthy")
	require.Contains(t, f.Detail, "migrate")
	require.Contains(t, f.Detail, "not ordering")

	list := importOf(t, `
services:
  api:
    image: x
    depends_on: [db, cache]
`)
	require.Contains(t, finding(t, list, "api", "depends_on").Detail, "db")
}

// TestTopLevelConcernsAreReported.
func TestTopLevelConcernsAreReported(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
networks:
  frontend: {}
secrets:
  api_key:
    file: ./secret.txt
configs:
  nginx:
    file: ./nginx.conf
`)
	require.Equal(t, importer.Ignored, finding(t, r, "", "networks").Level)
	require.Equal(t, importer.Blocked, finding(t, r, "", "secrets").Level)
	require.Equal(t, importer.Blocked, finding(t, r, "", "configs").Level)
}

// TestMetadataIsIgnoredRatherThanBlocked. Labels and profiles cost the import
// nothing, and reporting them at the same level as a missing health contract
// would bury the findings that matter.
func TestMetadataIsIgnoredRatherThanBlocked(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    labels:
      com.example.owner: platform
    profiles: [debug]
    expose: ["8080"]
`)
	for _, key := range []string{"labels", "profiles", "expose"} {
		require.Equal(t, importer.Ignored, finding(t, r, "api", key).Level, key)
	}
	require.Empty(t, r.Blocked())
}

// TestEveryServiceIsImported, and the findings are stable so a diff of two
// import runs is readable.
func TestEveryServiceIsImportedInAStableOrder(t *testing.T) {
	r := importOf(t, `
services:
  web:
    image: nginx
    restart: always
  api:
    image: acme/api
    restart: always
  db:
    image: postgres
    restart: always
`)
	require.Len(t, r.Resources, 3)

	var order []string
	for _, f := range r.Findings {
		order = append(order, f.Resource)
	}
	require.Equal(t, []string{"api", "db", "web"}, order, "sorted, so two runs diff cleanly")
}

func TestAFileWithNoServicesIsNotACompose(t *testing.T) {
	_, err := Import([]byte("version: '3'\n"))
	require.ErrorIs(t, err, ErrNotCompose)

	_, err = Import([]byte("this: [is not: valid yaml"))
	require.Error(t, err)
}

func TestAFindingReadsAsASentence(t *testing.T) {
	f := importer.Finding{Resource: "api", Key: "healthcheck", Level: importer.Blocked,
		Code: "NO_TAU_FEATURE:healthcheck", Needs: "health.{test,interval}", Detail: "no vocabulary"}
	require.Equal(t, "api: NO_TAU_FEATURE:healthcheck — no vocabulary (needs health.{test,interval})", f.String())

	top := importer.Finding{Key: "networks", Level: importer.Ignored, Code: "NO_TAU_FEATURE:networks", Detail: "not modelled"}
	require.Equal(t, "NO_TAU_FEATURE:networks — not modelled", top.String())
}

// TestARealisticComposeFileImportsAndReportsEverything — the shape of what
// somebody actually arrives with.
func TestARealisticComposeFileImportsAndReportsEverything(t *testing.T) {
	r := importOf(t, `
services:
  web:
    build: ./web
    ports:
      - "80:3000"
    environment:
      API_URL: http://api:8080
    depends_on:
      - api
    restart: unless-stopped
  api:
    image: acme/api:2
    command: ["./api", "--listen", ":8080"]
    environment:
      - DATABASE_URL=postgres://db:5432/app
    depends_on:
      db:
        condition: service_healthy
    restart: unless-stopped
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 10s
    restart: always
volumes:
  pgdata: {}
`)
	require.Len(t, r.Resources, 3)
	require.Equal(t, "acme/api:2", r.Resources["api"]["image"])
	require.NotNil(t, r.Resources["web"]["build"])

	// The honest report: this file cannot be imported faithfully, and the
	// person doing it should know exactly why before they rely on it.
	blocked := r.Blocked()
	require.NotEmpty(t, blocked)

	var keys []string
	for _, f := range blocked {
		keys = append(keys, f.Key)
	}
	require.Subset(t, keys, []string{"restart", "depends_on", "volumes", "healthcheck"})
}
