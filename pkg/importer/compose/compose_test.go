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

// pkgOf is the package a service runs.
func pkgOf(t *testing.T, r importer.Result, service string) map[string]any {
	t.Helper()
	ref, ok := docOf(t, r, service)["package"].(map[string]any)
	require.True(t, ok, "%s names no package", service)
	pkg, ok := r.Doc("package", ref["name"].(string))
	require.True(t, ok, "%s names %v, which was not imported", service, ref)
	return pkg
}

func TestTheTargetKindIsVerifiedAgainstTheVocabulary(t *testing.T) {
	kind, err := targetKind()
	require.NoError(t, err)
	require.Contains(t, v2.EnginesOf(kind), "oci",
		"the importer's target must be a kind the vocabulary says runs images")
}

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

	// What runs is a package, and an image is one of its sources.
	require.Equal(t, map[string]any{"name": "api_image"}, api["package"])
	require.Equal(t, map[string]any{"type": "oci", "source": map[string]any{"image": "acme/api:1.2.3"}}, pkgOf(t, r, "api"))

	// A VARIABLE IS A BLOCK, not a bare value: `at` says build or run and `as`
	// says how it is delivered, both defaulted by the vocabulary.
	require.Equal(t, map[string]any{
		"DATABASE_URL": map[string]any{"value": "postgres://db/app"},
		"LOG_LEVEL":    map[string]any{"value": "debug"},
	}, api["variables"])

	// A command and a working directory are not carried (TestACommandIsNotCarried).
	require.NotContains(t, api, "command")
	require.NotContains(t, api, "workdir")
	require.NotContains(t, api, "working_dir")
}

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

func TestABuildBecomesTheDockerfileBranchOfTheBuildStanza(t *testing.T) {
	r := importOf(t, `
services:
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.prod
      target: release
      args:
        VERSION: "1.4"
`)
	pkg := pkgOf(t, r, "api")

	// The context is the package's source, not part of the build.
	require.Equal(t, "api", pkg["source"])
	require.Equal(t, map[string]any{
		"dockerfile": map[string]any{
			"path":   "Dockerfile.prod",
			"target": "release",
		},
	}, pkg["build"])
	// and the build's arguments are the package's variables, which a Dockerfile build
	// takes as its ARGs
	require.Equal(t, map[string]any{"VERSION": map[string]any{"value": "1.4"}}, pkg["variables"])

	short := importOf(t, `
services:
  api:
    build: ./api
`)
	require.Equal(t, "api", pkgOf(t, short, "api")["source"])
	require.NotContains(t, docOf(t, short, "api"), "build", "a context alone says nothing about how")
}

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
	require.Equal(t, map[string]any{"image": "x"}, pkgOf(t, r, "api")["source"], "and what CAN be honoured is still there")

	// The named volume is a resource of its own and the service mounts it.
	require.Equal(t, map[string]any{"data": map[string]any{"ops": []string{"mount-rw"}, "path": "/var/lib/data"}},
		api["volume"])
}

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

	// The container port is carried under a name derived from its number; the host half
	// is not.
	require.Equal(t, map[string]any{
		"port_80":   map[string]any{"number": 80},
		"port_8443": map[string]any{"number": 8443},
	}, docOf(t, r, "api")["ports"])
}

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

	// Each service carries several findings, so the order is asserted: a service's
	// findings together, services sorted.
	var order []string
	for _, f := range r.Findings {
		if len(order) == 0 || order[len(order)-1] != f.Resource {
			order = append(order, f.Resource)
		}
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
	require.Equal(t, map[string]any{"image": "acme/api:2"}, pkgOf(t, r, "api")["source"])
	require.Equal(t, "web", pkgOf(t, r, "web")["source"])

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

func TestSizesAreRespelled(t *testing.T) {
	type testCase struct{ in, want string }
	for _, c := range []testCase{
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
	require.Equal(t, "CPUS_NOT_READABLE", finding(t, r, "api", "deploy.resources.limits.cpus").Code)

	// An unreadable size is reported and then defaulted: two findings on one field.
	var codes []string
	for _, f := range r.Findings {
		if f.Resource == "api" && f.Key == "deploy.resources.limits.memory" {
			codes = append(codes, f.Code)
		}
	}
	require.Contains(t, codes, "MEMORY_NOT_READABLE")
	require.Contains(t, codes, "MEMORY_DEFAULTED")

	execution, ok := docOf(t, r, "api")["execution"].(map[string]any)
	require.True(t, ok, "a ceiling is always written")
	require.Equal(t, defaultMemory, execution["memory"])
}

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
