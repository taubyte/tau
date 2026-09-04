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

// docOf is one imported application's document.
func docOf(t *testing.T, r importer.Result, name string) map[string]any {
	t.Helper()
	doc, ok := r.Doc("application", name)
	require.True(t, ok, "no application called %q; got %v", name, r.Names("application"))
	return doc
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
	api := docOf(t, r, "api")

	// `source` IS A BLOCK AND `image` IS ONE OF ITS BRANCHES. The first version
	// of this importer wrote `image:` at the top level, which is not a key this
	// vocabulary has, and nothing caught it until an emitter tried to compile
	// the result.
	require.Equal(t, map[string]any{"image": "acme/api:1.2.3"}, api["source"])

	// A VARIABLE IS A BLOCK, not a bare value: `at` says build or run and `as`
	// says how it is delivered, both defaulted by the vocabulary.
	require.Equal(t, map[string]any{
		"DATABASE_URL": map[string]any{"value": "postgres://db/app"},
		"LOG_LEVEL":    map[string]any{"value": "debug"},
	}, api["variables"])

	// And a command and a working directory are NOT carried, because there is
	// no field for either — see TestACommandIsNotCarried.
	require.NotContains(t, api, "command")
	require.NotContains(t, api, "workdir")
	require.NotContains(t, api, "working_dir")
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
	require.Equal(t, map[string]any{
		"A": map[string]any{"value": "1"},
		"B": map[string]any{"value": "two"},
	}, docOf(t, list, "api")["variables"])

	mapping := importOf(t, `
services:
  api:
    image: x
    environment:
      A: 1
      B: two
`)
	require.Equal(t, map[string]any{
		"A": map[string]any{"value": "1"},
		"B": map[string]any{"value": "two"},
	}, docOf(t, mapping, "api")["variables"])
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
	require.Equal(t, map[string]any{"REAL": map[string]any{"value": "value"}},
		docOf(t, r, "api")["variables"])
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
	api := docOf(t, r, "api")

	// THE CONTEXT IS `source` AND NOT PART OF THE BUILD. A build says HOW; where
	// in the repository is the resource's source, which is the field an
	// image-less workload uses anyway.
	require.Equal(t, "api", api["source"])
	require.Equal(t, map[string]any{
		"dockerfile": map[string]any{
			"path":   "Dockerfile.prod",
			"target": "release",
		},
	}, api["build"])

	short := importOf(t, `
services:
  api:
    build: ./api
`)
	require.Equal(t, "api", docOf(t, short, "api")["source"])
	require.NotContains(t, docOf(t, short, "api"), "build", "a context alone says nothing about how")
}

// TestAServiceWithNoSourceIsRefused rather than imported as an empty resource.
func TestAServiceWithNoSourceIsRefused(t *testing.T) {
	r := importOf(t, `
services:
  api:
    environment:
      A: 1
`)
	_, ok := r.Doc("application", "api")
	require.False(t, ok, "there is nothing to run")
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
		"stop_grace_period", "volumes../data:/var/lib/data", "env_file", "entrypoint", "depends_on",
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
	api := docOf(t, r, "api")
	for _, key := range []string{
		"restart", "healthcheck", "privileged", "cap_add", "devices",
		"network_mode", "deploy", "replicas", "command", "workdir", "image", "env",
	} {
		require.NotContains(t, api, key,
			"%s reached the config; the runtime cannot honour it and the next reader would believe it", key)
	}
	require.Equal(t, map[string]any{"image": "x"}, api["source"], "and what CAN be honoured is still there")

	// The named volume IS carried now — it is a resource of its own and the
	// service mounts it. See TestANamedVolumeBecomesAResource.
	require.Equal(t, map[string]any{"data": map[string]any{"ops": []string{"mount-rw"}, "path": "/var/lib/data"}},
		api["volume"])
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

	// THE CONTAINER PORT IS CARRIED and the host half is not. A tau port is
	// NAMED so everything else refers to it without repeating a number, and
	// compose ports have none — so the name is derived from the number,
	// deterministically, because a name that changed between two imports would
	// break every edge written against the first.
	require.Equal(t, map[string]any{
		"port_80":   map[string]any{"number": 80},
		"port_8443": map[string]any{"number": 8443},
	}, docOf(t, r, "api")["ports"])
}

// TestACommandIsNotCarried. There is no command field on a container workload:
// what runs is what the image says. This importer emitted one until the
// vocabulary was actually consulted.
func TestACommandIsNotCarried(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    command: ["serve", "--port", "8080"]
`)
	f := finding(t, r, "api", "command")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "NO_TAU_FEATURE:command", f.Code)
	require.Contains(t, f.Detail, "serve --port 8080")
	require.NotEmpty(t, f.Needs)
	require.NotContains(t, docOf(t, r, "api"), "command")
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
	require.Len(t, r.Names("application"), 3)

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
	require.Equal(t, []string{"api", "db", "web"}, r.Names("application"))
	require.Equal(t, map[string]any{"image": "acme/api:2"}, docOf(t, r, "api")["source"])
	require.Equal(t, "web", docOf(t, r, "web")["source"])

	// The database keeps its data, which is the difference between importing a
	// Postgres and importing a Postgres that loses everything on re-placement.
	require.Equal(t, []string{"pgdata"}, r.Names("volume"))
	require.Equal(t,
		map[string]any{"pgdata": map[string]any{"ops": []string{"mount-rw"}, "path": "/var/lib/postgresql/data"}},
		docOf(t, r, "db")["volume"])

	// The honest report: this file cannot be imported faithfully, and the
	// person doing it should know exactly why before they rely on it.
	blocked := r.Blocked()
	require.NotEmpty(t, blocked)

	var keys []string
	for _, f := range blocked {
		keys = append(keys, f.Key)
	}
	require.Subset(t, keys, []string{"restart", "depends_on", "healthcheck", "command"})
}

// TestANamedVolumeBecomesAResource. This is the difference between importing a
// Postgres and importing a Postgres that loses everything the first time it is
// re-placed, and it was reported as blocked until the vocabulary grew a volume
// kind and this importer was told.
func TestANamedVolumeBecomesAResource(t *testing.T) {
	r := importOf(t, `
services:
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
      - shared:/srv/shared:ro
volumes:
  pgdata: {}
  shared: {}
`)
	require.Equal(t, []string{"pgdata", "shared"}, r.Names("volume"))

	require.Equal(t, map[string]any{
		"pgdata": map[string]any{"ops": []string{"mount-rw"}, "path": "/var/lib/postgresql/data"},
		"shared": map[string]any{"ops": []string{"mount-ro"}, "path": "/srv/shared"},
	}, docOf(t, r, "db")["volume"])

	// SIZE IS REQUIRED AND COMPOSE DOES NOT SAY ONE. A compose volume grows
	// into whatever the host has; a placed one has to be asked for, and there
	// is no honest number to infer.
	f := finding(t, r, "pgdata", "volumes.pgdata")
	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "VOLUME_HAS_NO_SIZE", f.Code)
}

// TestComposeDeclaresAVolumeByUsingIt. `- data:/var/lib/data` with no top-level
// entry is a named volume; reading only the top-level block made every one of
// them a bind mount.
func TestAnImplicitlyDeclaredVolumeIsStillOne(t *testing.T) {
	r := importOf(t, `
services:
  db:
    image: postgres:16
    volumes:
      - data:/var/lib/data
`)
	require.Equal(t, []string{"data"}, r.Names("volume"))
	require.Contains(t, docOf(t, r, "db")["volume"], "data")
}

// TestABindMountIsHostStateAndDoesNotSurvivePlacement.
func TestABindMountIsRefused(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    volumes:
      - ./config:/etc/app
      - /var/run/docker.sock:/var/run/docker.sock
      - anonymous
`)
	require.Empty(t, r.Names("volume"))
	require.NotContains(t, docOf(t, r, "api"), "volume")

	for _, key := range []string{"volumes../config:/etc/app", "volumes./var/run/docker.sock:/var/run/docker.sock"} {
		f := finding(t, r, "api", key)
		require.Equal(t, importer.Blocked, f.Level, key)
		require.Equal(t, "BIND_MOUNT_IS_HOST_STATE", f.Code, key)
	}

	f := finding(t, r, "api", "volumes.anonymous")
	require.Equal(t, "NO_TAU_FEATURE:anonymous-volume", f.Code)
}

// TestResourceLimitsCarryAndReplicasDoNot. deploy.resources.limits is the one
// part with an equivalent; replicas is refused rather than dropped, because it
// is the field somebody looks for first and its absence is a decision.
func TestResourceLimitsCarry(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    deploy:
      replicas: 5
      resources:
        limits:
          memory: 512M
          cpus: "1.5"
        reservations:
          memory: 256M
`)
	require.Equal(t, map[string]any{"memory": "512MB", "cpu": 1500}, docOf(t, r, "api")["execution"])

	f := finding(t, r, "api", "deploy.replicas")
	require.Equal(t, importer.Blocked, f.Level)
	require.Contains(t, f.Detail, "5")

	require.Equal(t, importer.Blocked, finding(t, r, "api", "deploy.resources.reservations").Level)
}

// TestSizesAreRespelled. Compose writes 512M where the vocabulary writes 512MB,
// and both mean the same thing.
func TestSizesAreRespelled(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"512M", "512MB"},
		{"512MB", "512MB"},
		{"2G", "2GB"},
		{"1024", "1024B"},
		{"1024b", "1024B"},
		{" 2G ", "2GB"},
	} {
		require.Equal(t, c.want, bytesOf(c.in), c.in)
	}

	for _, bad := range []string{"", "lots", "M512", "512Q"} {
		require.Empty(t, bytesOf(bad), bad)
	}

	// And an unreadable one is reported rather than dropped.
	r := importOf(t, `
services:
  api:
    image: x
    deploy:
      resources:
        limits:
          memory: plenty
          cpus: "many"
`)
	require.Equal(t, "MEMORY_NOT_READABLE", finding(t, r, "api", "deploy.resources.limits.memory").Code)
	require.Equal(t, "CPUS_NOT_READABLE", finding(t, r, "api", "deploy.resources.limits.cpus").Code)
	require.NotContains(t, docOf(t, r, "api"), "execution")
}

// TestAPortSpecIsReadInEveryComposeSpelling.
func TestAPortSpecIsReadInEveryComposeSpelling(t *testing.T) {
	r := importOf(t, `
services:
  api:
    image: x
    ports:
      - "3000"
      - "8080:80"
      - "127.0.0.1:9090:9000"
      - "5000:5000/udp"
      - "8000-8010:8000-8010"
`)
	require.Equal(t, map[string]any{
		"port_3000": map[string]any{"number": 3000},
		"port_80":   map[string]any{"number": 80},
		"port_9000": map[string]any{"number": 9000},
		"port_5000": map[string]any{"number": 5000},
	}, docOf(t, r, "api")["ports"])

	// A range is not one port and is refused rather than half-read.
	require.Equal(t, "PORT_NOT_A_NUMBER", finding(t, r, "api", "ports.8000-8010:8000-8010").Code)
}

// TestANameThatIsNotAV2NameIsRenamedLoudly. A compose service may be called
// `my-api`; a v2 name may not. Renaming silently would produce config whose
// names do not match the user's dashboards, and the first sign would be an edge
// pointing at nothing.
func TestANameThatIsNotAV2NameIsRenamedLoudly(t *testing.T) {
	r := importOf(t, `
services:
  my-api:
    image: x
    environment:
      MY-VAR: "1"
    volumes:
      - pg-data:/var/lib/data
`)
	require.Equal(t, []string{"my_api"}, r.Names("application"))
	require.Equal(t, []string{"pg_data"}, r.Names("volume"))
	require.Contains(t, docOf(t, r, "my_api")["variables"], "MY_VAR")

	for _, key := range []string{"services", "environment.MY-VAR"} {
		f := finding(t, r, "my_api", key)
		require.Equal(t, importer.Approximated, f.Level, key)
		require.Equal(t, "NAME_NOT_A_V2_NAME", f.Code, key)
	}
}
