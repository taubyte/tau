// Package fly imports a fly.toml.
//
// # The file is the input; the Machines API is the truth
//
// importers.md is explicit: a Fly app's effective configuration is its machine
// config, which carries standbys, schedules, per-process secrets and a
// containers array a hand-written toml simply omits. So a file-only import of
// Fly is a good first pass and not the whole picture, and that is reported as an
// Elsewhere finding rather than left for somebody to discover.
//
// # One Fly app fans out into N tau resources
//
// `[processes]` is the main structural transform here. A Fly app with a web
// process and a worker process is ONE app sharing one filesystem and one
// network; in tau those are separate placed resources. The fan-out is a
// synthesis rather than an inference — the processes really did share
// everything, so every edge among the resulting resources is real — but the
// sharing itself does not survive, and a process that reached a sibling through
// localhost will not find it.
package fly

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/alecthomas/units"

	"github.com/taubyte/tau/pkg/importer"
)

// Source is what this importer is called in a finding.
const Source = "fly"

type file struct {
	App           string            `toml:"app"`
	PrimaryRegion string            `toml:"primary_region"`
	Build         *build            `toml:"build"`
	Env           map[string]string `toml:"env"`
	Processes     map[string]string `toml:"processes"`
	HTTPService   *httpService      `toml:"http_service"`
	VM            []vm              `toml:"vm"`
	Mounts        []mount           `toml:"mounts"`
	Statics       []static          `toml:"statics"`
	Deploy        *deploy           `toml:"deploy"`
	Checks        map[string]any    `toml:"checks"`
	Metrics       *metrics          `toml:"metrics"`
}

type build struct {
	Dockerfile  string            `toml:"dockerfile"`
	BuildTarget string            `toml:"build-target"`
	Image       string            `toml:"image"`
	Builder     string            `toml:"builder"`
	Args        map[string]string `toml:"args"`
	Ignorefile  string            `toml:"ignorefile"`
}

type httpService struct {
	InternalPort int          `toml:"internal_port"`
	ForceHTTPS   bool         `toml:"force_https"`
	AutoStop     any          `toml:"auto_stop_machines"`
	AutoStart    any          `toml:"auto_start_machines"`
	MinRunning   *int         `toml:"min_machines_running"`
	Concurrency  *concurrency `toml:"concurrency"`
	Checks       []any        `toml:"checks"`
}

type concurrency struct {
	Type      string `toml:"type"`
	SoftLimit int    `toml:"soft_limit"`
	HardLimit int    `toml:"hard_limit"`
}

type vm struct {
	Size      string   `toml:"size"`
	Memory    string   `toml:"memory"`
	CPUs      int      `toml:"cpus"`
	CPUKind   string   `toml:"cpu_kind"`
	GPUKind   string   `toml:"gpu_kind"`
	Processes []string `toml:"processes"`
}

type mount struct {
	Source      string `toml:"source"`
	Destination string `toml:"destination"`
	InitialSize string `toml:"initial_size"`
	Snapshots   *int   `toml:"snapshot_retention"`
}

type static struct {
	GuestPath     string `toml:"guest_path"`
	URLPrefix     string `toml:"url_prefix"`
	IndexDocument string `toml:"index_document"`
	TigrisBucket  string `toml:"tigris_bucket"`
}

type deploy struct {
	ReleaseCommand string `toml:"release_command"`
	Strategy       string `toml:"strategy"`
}

type metrics struct {
	Port int    `toml:"port"`
	Path string `toml:"path"`
}

// Import reads a fly.toml.
func Import(data []byte) (importer.Result, error) {
	var f file
	if err := toml.Unmarshal(data, &f); err != nil {
		return importer.Result{}, fmt.Errorf("fly: %w", err)
	}

	r := importer.Result{Source: Source}

	// THE FILE IS NOT THE EFFECTIVE CONFIGURATION, and saying so first is the
	// honest framing for everything below it.
	r.Add(importer.Finding{Key: "machines", Level: importer.Elsewhere,
		Code: "SOURCE_IN_API:machines",
		Detail: "a Fly app's effective config is its machine config, which carries standbys, schedules, " +
			"per-process secrets and a containers array this file omits"})
	r.Add(importer.Finding{Key: "secrets", Level: importer.Elsewhere,
		Code:   "SOURCE_WRITE_ONLY:secret",
		Detail: "fly secrets are set out of band and are write-only; this file never contains them"})

	source := f.App
	if source == "" {
		source = "app"
	}
	name, renamed := importer.Name(source)
	if renamed {
		r.Add(importer.Renamed(name, "app", source, name))
	}

	base := map[string]any{}
	if f.Build != nil {
		importBuild(&r, base, f.Build)
	}
	if len(f.Env) > 0 {
		// A VARIABLE IS A BLOCK, not a bare value: `at` says build or run and
		// `as` says how it is delivered, both defaulted by the vocabulary.
		variables := map[string]any{}
		for _, key := range sortedKeys(f.Env) {
			stem, renamed := importer.Name(key)
			if stem == "" {
				continue
			}
			if renamed {
				r.Add(importer.Renamed(name, "env."+key, key, stem))
			}
			variables[stem] = map[string]any{"value": f.Env[key]}
		}
		if len(variables) > 0 {
			base["variables"] = variables
		}
	}
	importVM(&r, base, f.VM)

	// AN APP THAT NAMES NOTHING TO RUN IS NOT EMITTED. A fly.toml can be a stub
	// — statics only, or a file whose build lives in the Machines API — and an
	// empty resource is one that runs nothing while looking like it runs
	// something. Everything else in the file is still reported, which is why
	// this sets a flag rather than returning early.
	_, hasSource := base["source"]
	_, hasBuild := base["build"]
	runnable := hasSource || hasBuild
	if !runnable {
		r.Add(importer.Finding{Key: "build", Level: importer.Blocked, Code: "NO_SOURCE",
			Detail: "this file names neither an image nor a Dockerfile build, so there is nothing to run; " +
				"a Fly app whose build is only in the Machines API looks exactly like this"})
	}

	// The fan-out. One app with N processes becomes N resources, each carrying
	// the app's build, env and limits, and differing only in command.
	switch {
	case !runnable:
		// nothing to emit
	case len(f.Processes) > 0:
		for _, p := range sortedKeys(f.Processes) {
			process, _ := importer.Name(name + "_" + p)
			r.Set(appKind, process, clone(base))

			// THE COMMAND IS WHAT MADE THEM DIFFERENT AND IT IS NOT CARRIED.
			// There is no command field on a container workload — what runs is
			// what the image says — so N processes become N resources that are
			// otherwise identical, which is worse than it looks and is said
			// here rather than discovered.
			r.Add(importer.Finding{Resource: process, Key: "processes." + p, Level: importer.Blocked,
				Code:   "NO_TAU_FEATURE:command",
				Needs:  "a command override on the compute kind",
				Detail: "the process ran \"" + f.Processes[p] + "\"; the image's own command is what runs here, so this resource is its sibling until that is fixed"})
		}
		r.Add(importer.Finding{Key: "processes", Level: importer.Approximated,
			Code: "APP_FANS_OUT",
			Detail: fmt.Sprintf("%d processes became %d resources; they shared one filesystem and one network on Fly, "+
				"so a process reaching a sibling over localhost will not find it", len(f.Processes), len(f.Processes))})
	default:
		r.Set(appKind, name, base)
	}

	importHTTP(&r, f.HTTPService)
	importStatics(&r, f.Statics)
	importMounts(&r, f.Mounts)
	importDeploy(&r, f.Deploy)

	if f.PrimaryRegion != "" {
		r.Add(importer.Finding{Key: "primary_region", Level: importer.Ignored,
			Code:   "IGNORED:region",
			Detail: "where a workload runs is the platform's decision; a region is a placement label at most"})
	}
	if len(f.Checks) > 0 {
		r.Add(importer.Finding{Key: "checks", Level: importer.Blocked,
			Code:  "NO_TAU_FEATURE:healthcheck",
			Needs: "health.{test,interval,timeout,grace,retries} plus a definition of what a failing check does",
			Detail: "fly writes the same field in three unit systems — duration strings, integer milliseconds and API " +
				"nanoseconds — and tau has no health-check vocabulary at all"})
	}
	if f.Metrics != nil {
		r.Add(importer.Finding{Key: "metrics", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:metrics-scrape",
			Needs:  "a scrape configuration on the compute kind",
			Detail: "a metrics port and path have nowhere to be declared"})
	}

	r.Sort()
	return r, nil
}

// appKind is what a Fly app becomes.
const appKind = "application"

func importBuild(r *importer.Result, doc map[string]any, b *build) {
	switch {
	case b.Image != "":
		doc["source"] = map[string]any{"image": b.Image}
	case b.Builder != "":
		// A CNB builder is a whole lifecycle, not a Dockerfile.
		r.Add(importer.Finding{Key: "build.builder", Level: importer.Blocked,
			Code:   "NEEDS_CNB_LIFECYCLE",
			Needs:  "a buildpack build path, or a re-derivation of the same build",
			Detail: "builder " + b.Builder + " is a Cloud Native Buildpacks lifecycle, which is not a Dockerfile build"})
	default:
		d := map[string]any{}
		if b.Dockerfile != "" {
			d["path"] = b.Dockerfile
		}
		if b.BuildTarget != "" {
			d["target"] = b.BuildTarget
		}
		if len(b.Args) > 0 {
			args := map[string]any{}
			for k, v := range b.Args {
				args[k] = v
			}
			d["args"] = args
		}
		if len(d) > 0 {
			doc["build"] = map[string]any{"dockerfile": d}
		}
	}
}

// flySize turns a Fly memory string into one tau's vocabulary parses.
//
// Fly writes "512mb", "1gb", or a BARE NUMBER, which its own documentation
// defines as megabytes. tau parses with units.ParseStrictBytes, which is
// case-sensitive — so every one of those three needs work and the bare number
// needs a unit invented for it, from Fly's rule rather than from a guess.
func flySize(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}

	digits := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if digits == -1 {
		// A bare number is megabytes, which is Fly's own default and not a
		// choice made here.
		s += "MB"
		digits = len(s) - 2
	}

	number, unit := s[:digits], strings.TrimSpace(s[digits:])
	if number == "" {
		return "", false
	}

	// "mb" -> "MB", "gib" -> "GiB": the binary units carry a lowercase "i"
	// and the rest are upper.
	upper := strings.ToUpper(unit)
	if len(upper) == 3 && upper[1] == 'I' {
		upper = string(upper[0]) + "i" + string(upper[2])
	}

	out := number + upper
	if _, err := units.ParseStrictBytes(out); err != nil {
		return "", false
	}
	return out, true
}

func importVM(r *importer.Result, doc map[string]any, vms []vm) {
	for _, v := range vms {
		if v.Memory != "" {
			// FLY'S SPELLING IS NOT TAU'S, and carrying it across unchanged
			// produced a project that IMPORTED and would not compile —
			// `512mb` against a parser that wants `512MB`. It is a
			// translation and not an approximation: the same number of bytes,
			// written the way this vocabulary writes it.
			if size, ok := flySize(v.Memory); ok {
				doc["execution"] = map[string]any{"memory": size}
			} else {
				r.Add(importer.Finding{Key: "vm.memory", Level: importer.Blocked,
					Code:   "SIZE_NOT_UNDERSTOOD",
					Detail: "memory " + v.Memory + " is not a size this importer can read, so the resource is emitted without one"})
			}
		}
		if v.Size != "" && v.Memory == "" {
			// A SIZE PRESET IS A NAME FOR TWO NUMBERS, and the numbers are what
			// tau has. Carrying the name would put a Fly word in the config.
			r.Add(importer.Finding{Key: "vm.size", Level: importer.Approximated,
				Code:   "SIZE_PRESET_NOT_EXPANDED",
				Detail: "size " + v.Size + " is a Fly name for a CPU and memory pair; tau declares the numbers, and this file gives neither"})
		}
		if v.CPUKind != "" || v.GPUKind != "" {
			r.Add(importer.Finding{Key: "vm.cpu_kind", Level: importer.Blocked,
				Code:   "REFUSED:hardware-class",
				Detail: "a CPU or GPU class is a hardware request, and refused until there is a placement vocabulary for it"})
		}
	}
}

func importHTTP(r *importer.Result, h *httpService) {
	if h == nil {
		return
	}
	if h.InternalPort != 0 {
		r.Add(importer.Finding{Key: "http_service", Level: importer.Approximated,
			Code: "ROUTING_BECOMES_AN_EDGE",
			Detail: fmt.Sprintf("internal_port %d is the target's own listener, not an edge field; what carries across is an inbound edge at mount /",
				h.InternalPort)})
	}
	if h.Concurrency != nil {
		r.Add(importer.Finding{Key: "http_service.concurrency", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:concurrency-policy",
			Needs:  "a concurrency policy naming soft and hard limits and what happens at each",
			Detail: "tau has a per-instance concurrency number and no policy around it"})
	}
	if h.MinRunning != nil || h.AutoStop != nil || h.AutoStart != nil {
		// DEGRADE, not map. Fly stops pre-existing machines and never changes
		// the count; tau creates and destroys.
		r.Add(importer.Finding{Key: "auto_stop_machines", Level: importer.Approximated,
			Code: "SCALING_MODEL_DIFFERS",
			Detail: "Fly starts and stops machines it already has; tau creates and destroys them, so min_machines_running " +
				"and the auto_start/auto_stop pair have no equivalent and are dropped"})
	}
	if len(h.Checks) > 0 {
		r.Add(importer.Finding{Key: "http_service.checks", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:healthcheck",
			Needs:  "health.{test,interval,timeout,grace,retries}",
			Detail: "tau has no health-check vocabulary"})
	}
}

func importStatics(r *importer.Result, statics []static) {
	for _, s := range statics {
		if s.TigrisBucket != "" {
			r.Add(importer.Finding{Key: "statics.tigris_bucket", Level: importer.Blocked,
				Code:   "NEEDS_STORAGE_AS_S3",
				Needs:  "an S3-compatible face on storage",
				Detail: "a static served straight from a bucket needs storage to answer S3"})
			continue
		}
		// A FLY STATIC IS A DIRECTORY INSIDE THE IMAGE, and a v2 website is a
		// repository built into one. They are not the same thing: there is
		// nothing here to build, and the files are already inside an artifact
		// somebody else made. Emitting a website whose build says nothing
		// would produce a site that serves nothing.
		r.Add(importer.Finding{Key: "statics." + s.URLPrefix, Level: importer.Blocked,
			Code:  "STATIC_IS_INSIDE_THE_IMAGE",
			Needs: "serving a path out of a container image, or a build that produces it",
			Detail: "guest_path " + s.GuestPath + " is a directory inside the app's own image; a website here is a " +
				"repository built into one, so there is nothing to point at"})
	}
}

func importMounts(r *importer.Result, mounts []mount) {
	for _, m := range mounts {
		r.Add(importer.Finding{Key: "mounts." + m.Source, Level: importer.Blocked,
			Code:  "MOUNT_IS_PER_PROCESS_GROUP",
			Needs: "nothing — a volume kind exists; which workload mounts it is what this file does not say",
			Detail: "volume " + m.Source + " at " + m.Destination + " is attached to a process group, and this file " +
				"does not say which workload should mount it here"})
		if m.Snapshots != nil {
			r.Add(importer.Finding{Key: "mounts.snapshot_retention", Level: importer.Blocked,
				Code:   "NO_TAU_FEATURE:snapshots",
				Needs:  "the volume snapshot chain",
				Detail: "snapshot retention is the backup product, and it needs volumes first"})
		}
	}
}

func importDeploy(r *importer.Result, d *deploy) {
	if d == nil {
		return
	}
	if d.ReleaseCommand != "" {
		r.Add(importer.Finding{Key: "deploy.release_command", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:release-command",
			Needs:  "a one-shot invocation that runs once per deploy and gates it",
			Detail: "release_command " + d.ReleaseCommand + " is how a migration runs; without it, importing anything with a migration is broken"})
	}
	if d.Strategy != "" {
		r.Add(importer.Finding{Key: "deploy.strategy", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:deploy-strategy",
			Needs:  "a deploy strategy, which for canary is a promote pointer of {primary, canary, weight}",
			Detail: "strategy " + d.Strategy + " has no equivalent; tau decides how a rollout proceeds"})
	}
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
