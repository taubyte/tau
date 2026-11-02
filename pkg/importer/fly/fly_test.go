package fly

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
)

func importOf(t *testing.T, s string) importer.Result {
	t.Helper()
	r, err := Import([]byte(s))
	require.NoError(t, err)
	return r
}

func finding(t *testing.T, r importer.Result, key string) importer.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q", key)
	return importer.Finding{}
}

// docOf is one imported application's document.
func docOf(t *testing.T, r importer.Result, name string) map[string]any {
	t.Helper()
	doc, ok := r.Doc("application", name)
	require.True(t, ok, "no application called %q; got %v", name, r.Names("application"))
	return doc
}

func has(r importer.Result, key string) bool {
	for _, f := range r.Findings {
		if f.Key == key {
			return true
		}
	}
	return false
}

func TestTheFileIsTheInputAndTheApiIsTheTruth(t *testing.T) {
	r := importOf(t, `app = "shop"
[build]
  image = "acme/shop:1"`)

	f := finding(t, r, "machines")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Empty(t, f.Needs, "there is nothing for tau to build; the fix is a token")
	require.Contains(t, f.Detail, "machine config")

	require.Equal(t, importer.Elsewhere, finding(t, r, "secrets").Level)
}

func TestWhatCarriesAcross(t *testing.T) {
	r := importOf(t, `
app = "shop"

[build]
  dockerfile = "Dockerfile.prod"
  build-target = "release"

[build.args]
  NODE_ENV = "production"

[env]
  DATABASE_URL = "postgres://db/app"

[[vm]]
  memory = "512mb"
`)
	doc := docOf(t, r, "shop")
	require.Equal(t, map[string]any{
		"dockerfile": map[string]any{
			"path":   "Dockerfile.prod",
			"target": "release",
			"args":   map[string]any{"NODE_ENV": "production"},
		},
	}, doc["build"])

	// A VARIABLE IS A BLOCK, and a limit lives under `execution` — both are
	// where the vocabulary puts them, not where fly.toml does.
	require.Equal(t, map[string]any{"DATABASE_URL": map[string]any{"value": "postgres://db/app"}}, doc["variables"])

	// The size is respelled: "512mb" would import and not compile.
	require.Equal(t, map[string]any{"memory": "512MB"}, doc["execution"])
}

func TestFlySizesAreRespelled(t *testing.T) {
	type testCase struct{ in, want string }
	for _, c := range []testCase{
		{"512mb", "512MB"},
		{"512MB", "512MB"},
		{"1gb", "1GB"},
		{"2GiB", "2GiB"},
		{"2gib", "2GiB"},
		{" 256mb ", "256MB"},
		// A BARE NUMBER IS MEGABYTES, which is Fly's own rule and not a guess.
		{"2048", "2048MB"},
	} {
		got, ok := flySize(c.in)
		require.True(t, ok, c.in)
		require.Equal(t, c.want, got, c.in)
	}

	for _, bad := range []string{"", "lots", "mb512", "512qb", "  "} {
		_, ok := flySize(bad)
		require.False(t, ok, bad)
	}
}

func TestAnUnreadableSizeIsReported(t *testing.T) {
	r := importOf(t, `
app = "shop"

[build]
  image = "ghcr.io/acme/shop:1"

[[vm]]
  memory = "plenty"
`)
	require.Equal(t, "SIZE_NOT_UNDERSTOOD", finding(t, r, "vm.memory").Code)
	require.NotContains(t, docOf(t, r, "shop"), "execution",
		"a value the compiler would refuse is never written")
}

func TestOneAppFansOutIntoNResources(t *testing.T) {
	r := importOf(t, `
app = "shop"

[build]
  image = "acme/shop:1"

[processes]
  web = "npm start"
  worker = "npm run worker"
`)
	require.Equal(t, []string{"shop_web", "shop_worker"}, r.Names("application"))
	require.Equal(t, map[string]any{"image": "acme/shop:1"}, docOf(t, r, "shop_web")["source"],
		"each carries the app's build")

	// The command is not carried, so N processes become N otherwise identical resources.
	for _, process := range []string{"shop_web", "shop_worker"} {
		require.NotContains(t, docOf(t, r, process), "command", process)
	}
	require.Equal(t, "NO_TAU_FEATURE:command", finding(t, r, "processes.web").Code)

	f := finding(t, r, "processes")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "localhost",
		"a process that reached a sibling over localhost will not find it, and that must be said")
}

func TestASizePresetIsNotCarriedAsAName(t *testing.T) {
	r := importOf(t, `
app = "shop"
[[vm]]
  size = "shared-cpu-1x"
`)
	// The app names nothing to run, so nothing is emitted — the size is still
	// reported, which is why this file's flag exists rather than an early
	// return.
	require.Empty(t, r.Names("application"))
	f := finding(t, r, "vm.size")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "shared-cpu-1x")
}

func TestHardwareClassesAreRefused(t *testing.T) {
	r := importOf(t, `
app = "shop"
[[vm]]
  cpu_kind = "performance"
  gpu_kind = "a100-40gb"
`)
	f := finding(t, r, "vm.cpu_kind")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "REFUSED:hardware-class", f.Code)
}

func TestABuildpackBuilderIsNotADockerfile(t *testing.T) {
	r := importOf(t, `
app = "shop"
[build]
  builder = "paketobuildpacks/builder:base"
`)
	f := finding(t, r, "build.builder")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "NEEDS_CNB_LIFECYCLE", f.Code)
	require.NotEmpty(t, f.Needs)
	_, ok := r.Doc("application", "shop")
	require.False(t, ok, "a builder is not a source, so there is nothing to run")
}

func TestTheScalingModelDiffersAndSaysSo(t *testing.T) {
	r := importOf(t, `
app = "shop"
[http_service]
  internal_port = 8080
  auto_stop_machines = true
  auto_start_machines = true
  min_machines_running = 2
`)
	f := finding(t, r, "auto_stop_machines")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "creates and destroys")

	require.Equal(t, "ROUTING_BECOMES_AN_EDGE", finding(t, r, "http_service").Code)
}

func TestAReleaseCommandIsNamedAsTheMigrationPathItIs(t *testing.T) {
	r := importOf(t, `
app = "shop"
[deploy]
  release_command = "npm run migrate"
  strategy = "bluegreen"
`)
	rc := finding(t, r, "deploy.release_command")
	require.Equal(t, importer.Blocked, rc.Level)
	require.Contains(t, rc.Detail, "migration")
	require.NotEmpty(t, rc.Needs)

	st := finding(t, r, "deploy.strategy")
	require.Equal(t, importer.Blocked, st.Level)
	require.Contains(t, st.Needs, "promote pointer")
}

func TestVolumesAndSnapshotsAreBothNamed(t *testing.T) {
	r := importOf(t, `
app = "shop"
[[mounts]]
  source = "data"
  destination = "/var/lib/data"
  snapshot_retention = 7
`)
	// A VOLUME KIND EXISTS NOW, and this is still blocked for a different
	// reason: a Fly mount is attached to a process group, and the file does not
	// say which workload should mount it here.
	m := finding(t, r, "mounts.data")
	require.Equal(t, importer.Blocked, m.Level)
	require.Equal(t, "MOUNT_IS_PER_PROCESS_GROUP", m.Code)
	require.Contains(t, m.Detail, "/var/lib/data")

	s := finding(t, r, "mounts.snapshot_retention")
	require.Contains(t, s.Needs, "snapshot chain")
}

func TestAStaticIsNotAWebsite(t *testing.T) {
	r := importOf(t, `
app = "shop"
[[statics]]
  guest_path = "/app/public"
  url_prefix = "/assets"
  index_document = "index.html"
`)
	require.Empty(t, r.Names("website"))

	f := finding(t, r, "statics./assets")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "STATIC_IS_INSIDE_THE_IMAGE", f.Code)
	require.Contains(t, f.Detail, "/app/public")
}

func TestATigrisBackedStaticNeedsS3(t *testing.T) {
	r := importOf(t, `
app = "shop"
[build]
  image = "acme/shop:1"
[[statics]]
  url_prefix = "/assets"
  tigris_bucket = "my-bucket"
`)
	require.Equal(t, "NEEDS_STORAGE_AS_S3", finding(t, r, "statics.tigris_bucket").Code)
	require.NotContains(t, r.Resources, "static-assets",
		"nothing is emitted for a static tau cannot serve")
}

func TestHealthChecksNameTheirThreeUnitSystems(t *testing.T) {
	r := importOf(t, `
app = "shop"
[checks]
  [checks.alive]
    type = "tcp"
    interval = "10s"
`)
	f := finding(t, r, "checks")
	require.Equal(t, importer.Blocked, f.Level)
	require.Contains(t, f.Detail, "three unit systems")
}

func TestConcurrencyPolicyIsBlocked(t *testing.T) {
	r := importOf(t, `
app = "shop"
[http_service]
  internal_port = 8080
[http_service.concurrency]
  type = "requests"
  soft_limit = 200
  hard_limit = 250
`)
	require.Equal(t, importer.Blocked, finding(t, r, "http_service.concurrency").Level)
}

func TestRegionIsIgnoredNotBlocked(t *testing.T) {
	r := importOf(t, `
app = "shop"
primary_region = "iad"
[build]
  image = "acme/shop:1"
`)
	require.Equal(t, importer.Ignored, finding(t, r, "primary_region").Level)
}

func TestAnAppWithNoNameStillImports(t *testing.T) {
	r := importOf(t, `
[build]
  image = "acme/thing:1"
`)
	require.Equal(t, []string{"app"}, r.Names("application"))
}

func TestAnAppNameIsSanitised(t *testing.T) {
	r := importOf(t, `
app = "my-shop"
[build]
  image = "acme/thing:1"
`)
	require.Equal(t, []string{"my_shop"}, r.Names("application"))

	f := finding(t, r, "app")
	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "NAME_NOT_A_V2_NAME", f.Code)
}

func TestMalformedTomlIsRefused(t *testing.T) {
	_, err := Import([]byte("app = "))
	require.Error(t, err)
}

func TestAMinimalFileReportsOnlyWhatIsElsewhere(t *testing.T) {
	r := importOf(t, `app = "shop"
[build]
  image = "acme/shop:1"`)
	require.Empty(t, r.Blocked())
	require.Len(t, r.Elsewhere(), 2)
	require.False(t, has(r, "processes"))
	require.Equal(t, Source, r.Source)
}

func TestAnAppThatNamesNothingToRunIsNotEmitted(t *testing.T) {
	r := importOf(t, `app = "shop"`)
	require.Empty(t, r.Resources)
	require.Equal(t, "NO_SOURCE", finding(t, r, "build").Code)

	// And the rest of the file is still reported.
	withStatics := importOf(t, `
app = "shop"
[[statics]]
  url_prefix = "/assets"
  tigris_bucket = "my-bucket"
`)
	require.Empty(t, withStatics.Resources)
	require.Equal(t, "NEEDS_STORAGE_AS_S3", finding(t, withStatics, "statics.tigris_bucket").Code)
}
