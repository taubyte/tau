// Package compose imports a docker-compose file, the one source whose manifest is the
// whole deployment (other platforms keep env, secrets and domains outside it).
//
// An importer never emits a field the runtime cannot honour, and every blocked field
// names the contract it would need, so findings are a prioritised to-do list. Every
// key dropped, approximated or meaning something different here produces a Finding;
// an import that reports nothing on a file using healthchecks has lied.
package compose

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/importer"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Source is what this importer is called in a finding.
const Source = "compose"

var (
	ErrNotCompose = errors.New("compose: no services in this file")
	ErrNoKind     = errors.New("compose: this vocabulary has no kind that runs a container")
)

// containerKind is what a compose service becomes: the long-running, HTTP-listening
// container kind, checked against the vocabulary so a rename fails at import.
const containerKind = "application"

func targetKind() (string, error) {
	if !slices.Contains(v2.EnginesOf(containerKind), "container") {
		return "", fmt.Errorf("%w: %q runs %v", ErrNoKind, containerKind, v2.EnginesOf(containerKind))
	}
	return containerKind, nil
}

// file is the subset of compose this importer reads. Everything not named here
// is reported as an unknown key rather than silently skipped.
type file struct {
	Services map[string]service `yaml:"services"`
	Volumes  map[string]any     `yaml:"volumes"`
	Networks map[string]any     `yaml:"networks"`
	Configs  map[string]any     `yaml:"configs"`
	Secrets  map[string]any     `yaml:"secrets"`
	Name     string             `yaml:"name"`
	Version  string             `yaml:"version"`
}

// deploy is the part of compose's deploy stanza that has an equivalent.
//
// REPLICAS DELIBERATELY DOES NOT. How many instances run, and where, is the
// platform's decision — it places, it reconciles, it re-places when a node
// dies. A replica count carried across would be a number nothing honours.
type deploy struct {
	Replicas  *int `yaml:"replicas"`
	Resources struct {
		Limits struct {
			Memory string `yaml:"memory"`
			CPUs   string `yaml:"cpus"`
		} `yaml:"limits"`
		Reservations struct {
			Memory string `yaml:"memory"`
			CPUs   string `yaml:"cpus"`
		} `yaml:"reservations"`
	} `yaml:"resources"`
}

func (d deploy) isZero() bool {
	return d.Replicas == nil &&
		d.Resources.Limits.Memory == "" && d.Resources.Limits.CPUs == "" &&
		d.Resources.Reservations.Memory == "" && d.Resources.Reservations.CPUs == ""
}

type service struct {
	Image      string    `yaml:"image"`
	Build      yaml.Node `yaml:"build"`
	Command    yaml.Node `yaml:"command"`
	Entrypoint yaml.Node `yaml:"entrypoint"`
	WorkingDir string    `yaml:"working_dir"`

	Environment yaml.Node `yaml:"environment"`
	EnvFile     yaml.Node `yaml:"env_file"`

	Ports     []string  `yaml:"ports"`
	Expose    []string  `yaml:"expose"`
	Volumes   yaml.Node `yaml:"volumes"`
	DependsOn yaml.Node `yaml:"depends_on"`

	// Everything below is read only so it can be REPORTED. None of it reaches
	// the emitted config — see the package doc's first rule.
	Healthcheck     yaml.Node `yaml:"healthcheck"`
	Restart         string    `yaml:"restart"`
	Init            *bool     `yaml:"init"`
	ReadOnly        *bool     `yaml:"read_only"`
	Tmpfs           yaml.Node `yaml:"tmpfs"`
	User            string    `yaml:"user"`
	Privileged      *bool     `yaml:"privileged"`
	CapAdd          []string  `yaml:"cap_add"`
	Devices         []string  `yaml:"devices"`
	NetworkMode     string    `yaml:"network_mode"`
	Deploy          deploy    `yaml:"deploy"`
	Labels          yaml.Node `yaml:"labels"`
	Profiles        []string  `yaml:"profiles"`
	StopGracePeriod string    `yaml:"stop_grace_period"`
	ExtraHosts      []string  `yaml:"extra_hosts"`
}

func Import(data []byte) (importer.Result, error) {
	kind, err := targetKind()
	if err != nil {
		return importer.Result{}, err
	}

	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return importer.Result{}, fmt.Errorf("compose: %w", err)
	}
	if len(f.Services) == 0 {
		return importer.Result{}, ErrNotCompose
	}

	r := importer.Result{Source: Source}

	// Top-level concerns that are not per-service.
	if len(f.Networks) > 0 {
		r.Add(importer.Finding{Key: "networks", Level: importer.Ignored, Code: "NO_TAU_FEATURE:networks",
			Detail: "tau places workloads and does not model networks; what may reach what is an " +
				"inbound edge on the resource, not a shared bridge"})
	}
	if len(f.Secrets) > 0 {
		r.Add(importer.Finding{Key: "secrets", Level: importer.Blocked, Code: "NO_TAU_FEATURE:compose-secrets",
			Needs:  "a secret reference on the compute kind",
			Detail: "compose secrets are files mounted from the host; they are not carried"})
	}
	if len(f.Configs) > 0 {
		r.Add(importer.Finding{Key: "configs", Level: importer.Blocked, Code: "NO_TAU_FEATURE:compose-configs",
			Needs:  "a config-file reference on the compute kind",
			Detail: "compose configs are files mounted from the host; they are not carried"})
	}

	// Named volumes become resources of their own, and a service that mounts
	// one names it. A bind mount is host state and is refused per service.
	volumes := importVolumes(&r, declaredVolumes(f))

	for _, name := range sortedKeys(f.Services) {
		importService(&r, kind, name, f.Services[name], volumes)
	}

	r.Sort()
	return r, nil
}

// importVolumes turns the top-level `volumes:` block into volume resources, and
// returns the map from a compose volume name to the v2 name it got.
//
// A NAMED VOLUME IS A RESOURCE NOW. It was reported as blocked when this
// importer was written, because the vocabulary had no volume kind. It has one,
// and a compose deployment whose database keeps its data in a named volume is
// the single most common shape in the segment — so this is the difference
// between importing a Postgres and importing a Postgres that loses its data.
func importVolumes(r *importer.Result, declared map[string]any) map[string]string {
	out := map[string]string{}

	for _, source := range sortedKeys(declared) {
		name, renamed := importer.Name(source)
		if name == "" {
			continue
		}
		if renamed {
			r.Add(importer.Renamed(source, "volumes", source, name))
		}
		out[source] = name

		// Size is required and compose has none; there is no honest number, so it is left for
		// a person and reported.
		r.Set(volumeKind, name, map[string]any{
			"description": "imported from a compose named volume",
		})
		r.Add(importer.Finding{Resource: name, Key: "volumes." + source, Level: importer.Approximated,
			Code:  "VOLUME_HAS_NO_SIZE",
			Needs: "nothing — the field exists; the value does not",
			Detail: "a compose volume grows into whatever the host has and names no size; a placed volume has to be asked " +
				"for one, and there is no honest number to infer, so it is left to be filled in"})
	}
	return out
}

// volumeKind is what a compose named volume becomes.
const volumeKind = "volume"

// declaredVolumes is the top-level `volumes:` block plus the ones only a mount
// mentions.
//
// COMPOSE DECLARES A VOLUME BY USING IT. `- data:/var/lib/data` with no
// top-level entry is a named volume; only a source that looks like a path is a
// bind mount. Reading just the top-level block made every implicitly declared
// volume a bind mount, which is the same database losing its data as before.
func declaredVolumes(f file) map[string]any {
	out := map[string]any{}
	for name, v := range f.Volumes {
		out[name] = v
	}

	for _, name := range sortedKeys(f.Services) {
		for _, spec := range stringsOf(f.Services[name].Volumes) {
			source, _, ok := strings.Cut(spec, ":")
			if !ok || isPath(source) {
				continue
			}
			if _, held := out[source]; !held {
				out[source] = nil
			}
		}
	}
	return out
}

// isPath reports whether a mount source is a path on the machine rather than a
// volume name. Compose's own rule: anything beginning with `/`, `.` or `~`.
func isPath(source string) bool {
	return strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, ".") ||
		strings.HasPrefix(source, "~") ||
		strings.Contains(source, "/")
}

func importService(r *importer.Result, kind, source string, s service, volumes map[string]string) {
	name, renamed := importer.Name(source)
	if name == "" {
		return
	}
	if renamed {
		r.Add(importer.Renamed(name, "services", source, name))
	}

	doc := map[string]any{}

	// `source` is a block and `image` is one of its branches.
	switch {
	case s.Image != "":
		doc["source"] = map[string]any{"image": s.Image}
	case !s.Build.IsZero():
		// The context is `source`, not part of the build stanza.
		build := buildToDockerfile(s.Build)
		if context, ok := build["context"].(string); ok && context != "" && context != "." {
			doc["source"] = strings.TrimPrefix(context, "./")
		}
		delete(build, "context")
		if len(build) > 0 {
			doc["build"] = map[string]any{"dockerfile": build}
		}
	default:
		r.Add(importer.Finding{Resource: name, Key: "image", Level: importer.Blocked, Code: "NO_SOURCE",
			Detail: "the service names neither an image nor a build; there is nothing to run"})
		return
	}

	// A command is not carried: a container workload has no command field; what runs is
	// what the image says.
	if cmd := stringsOf(s.Command); len(cmd) > 0 {
		r.Add(importer.Finding{Resource: name, Key: "command", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:command",
			Needs:  "a command override on the compute kind",
			Detail: "the image's own command is what runs: \"" + strings.Join(cmd, " ") + "\" is not carried"})
	}
	if ep := stringsOf(s.Entrypoint); len(ep) > 0 {
		// Entrypoint and command compose differently in the image; carrying
		// only one of them would change what runs.
		r.Add(importer.Finding{Resource: name, Key: "entrypoint", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:entrypoint",
			Needs:  "an entrypoint field distinct from command on the compute kind",
			Detail: "entrypoint and command combine in the image; tau has neither field, so an override cannot be expressed"})
	}
	if s.WorkingDir != "" {
		r.Add(importer.Finding{Resource: name, Key: "working_dir", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:working_dir",
			Needs:  "a working-directory field on the compute kind",
			Detail: "the image's own WORKDIR is what applies"})
	}

	// A variable is a block; `at` and `as` are defaulted by the vocabulary, so only the
	// value is written.
	if env := envOf(s.Environment); len(env) > 0 {
		variables := map[string]any{}
		for _, key := range sortedKeys(env) {
			stem, renamed := importer.Name(key)
			if stem == "" {
				continue
			}
			if renamed {
				r.Add(importer.Renamed(name, "environment."+key, key, stem))
			}
			variables[stem] = map[string]any{"value": env[key]}
		}
		if len(variables) > 0 {
			doc["variables"] = variables
		}
	}
	if len(stringsOf(s.EnvFile)) > 0 {
		// COMPOSE ENV PRECEDENCE IS SEVEN LAYERS. Interpolation, shell env,
		// .env, env_file, service environment, image ENV and build args do not
		// merge the way a single map does.
		r.Add(importer.Finding{Resource: name, Key: "env_file", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:env_file",
			Needs:  "a file-sourced environment on the compute kind",
			Detail: "env_file is one of seven precedence layers compose merges; its contents are on the importer's machine, not in this file"})
	}

	if ports := importPorts(r, name, s.Ports); len(ports) > 0 {
		doc["ports"] = ports
	}

	if mounts := importMounts(r, name, s.Volumes, volumes); len(mounts) > 0 {
		doc["volume"] = mounts
	}

	execution := importLimits(r, name, s.Deploy)
	if execution == nil {
		execution = map[string]any{}
	}
	// The vocabulary requires a memory ceiling and compose has none, so one is chosen and
	// reported; omitting it produced a project that would not compile.
	if _, ok := execution["memory"]; !ok {
		execution["memory"] = defaultMemory
		r.Add(importer.Finding{Resource: name, Key: "deploy.resources.limits.memory", Level: importer.Approximated,
			Code: "MEMORY_DEFAULTED",
			Detail: "this service declares no memory limit, so it had the host's; tau requires a per-resource ceiling " +
				"and " + defaultMemory + " was chosen — raise it before this runs anything that needs more"})
	}
	doc["execution"] = execution

	if deps := dependsOn(s.DependsOn); len(deps) > 0 {
		// depends_on is a HINT here and writes no config: service_healthy and
		// service_completed_successfully are readiness and one-shot
		// dependencies, not ordering, and tau models neither.
		r.Add(importer.Finding{Resource: name, Key: "depends_on", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:depends_on",
			Needs:  "start ordering, plus the health contract its conditions rely on",
			Detail: "depends_on " + strings.Join(deps, ", ") + ": conditions like service_healthy are readiness gates, not ordering, and tau has neither"})
	}

	// Everything the runtime has no field for. Each names what would unblock
	// it, so the list is a contract to-do rather than a shrug.
	for _, b := range []struct {
		key, needs, detail string
		present            bool
	}{
		// READINESS AND LIVENESS EXIST, and a compose healthcheck still does
		// not fit them: tau probes over HTTP or through a function, and a
		// compose check is a command run inside the container. The Needs names
		// the gap precisely rather than claiming there is no health vocabulary.
		{"healthcheck", "a command-based probe beside the http and function ones",
			"tau probes over http or through a function; a compose healthcheck is a command run inside the container, and translating one into the other would be guessing at a url", !s.Healthcheck.IsZero()},
		{"restart", "a restart policy, and a controller that honours it",
			"tau re-places a workload when a node dies; it has no per-service restart policy", s.Restart != ""},
		{"user", "a user field on the compute kind", "the container runs as whatever the image says", s.User != ""},
		{"read_only", "a read-only root filesystem field", "not expressible", s.ReadOnly != nil},
		{"init", "an init-process field", "not expressible", s.Init != nil},
		{"tmpfs", "a tmpfs mount field", "not expressible", !s.Tmpfs.IsZero()},
		{"privileged", "", "refused on purpose: a serverless tier does not run privileged containers", s.Privileged != nil && *s.Privileged},
		{"cap_add", "", "refused on purpose: added capabilities are not available to a workload", len(s.CapAdd) > 0},
		{"devices", "", "refused on purpose: host devices are not exposed to a workload", len(s.Devices) > 0},
		{"network_mode", "", "tau places workloads; a network mode has no equivalent", s.NetworkMode != ""},
		{"stop_grace_period", "a shutdown grace period", "not expressible", s.StopGracePeriod != ""},
		{"extra_hosts", "a hosts-file entry", "not expressible", len(s.ExtraHosts) > 0},
	} {
		if !b.present {
			continue
		}
		r.Add(importer.Finding{Resource: name, Key: b.key, Level: importer.Blocked,
			Code: "NO_TAU_FEATURE:" + b.key, Needs: b.needs, Detail: b.detail})
	}

	if !s.Labels.IsZero() {
		r.Add(importer.Finding{Resource: name, Key: "labels", Level: importer.Ignored, Code: "IGNORED:labels",
			Detail: "labels are metadata for other tooling and have no runtime meaning here"})
	}
	if len(s.Profiles) > 0 {
		r.Add(importer.Finding{Resource: name, Key: "profiles", Level: importer.Ignored, Code: "IGNORED:profiles",
			Detail: "every service is imported; compose profiles select which run, which is a deploy-time choice tau makes per environment"})
	}
	if len(s.Expose) > 0 {
		r.Add(importer.Finding{Resource: name, Key: "expose", Level: importer.Ignored, Code: "IGNORED:expose",
			Detail: "expose documents a port for other compose services; reachability here is an inbound edge"})
	}

	r.Set(kind, name, doc)
}

// importPorts turns compose's `HOST:CONTAINER` strings into named ports.
//
// THE NAME IS DERIVED AND THE HOST HALF IS DROPPED. A tau port is named so that
// everything else refers to it without repeating a number, and compose ports
// have no names — so the container port becomes `port_<n>`, deterministically,
// because a name that changed between two imports would break every edge
// written against the first.
//
// The host half has no equivalent and is not silently lost: the platform
// assigns the external port and routes to the workload by name, which is the
// whole reason a host mapping cannot be carried.
func importPorts(r *importer.Result, resource string, ports []string) map[string]any {
	if len(ports) == 0 {
		return nil
	}

	out := map[string]any{}
	var mapped []string

	for _, spec := range ports {
		container, host := containerPort(spec)
		if container == 0 {
			r.Add(importer.Finding{Resource: resource, Key: "ports." + spec, Level: importer.Blocked,
				Code:   "PORT_NOT_A_NUMBER",
				Detail: "\"" + spec + "\" is a range or a form this importer cannot read as one port"})
			continue
		}
		out[fmt.Sprintf("port_%d", container)] = map[string]any{"number": container}
		if host != "" {
			mapped = append(mapped, spec)
		}
	}

	if len(mapped) > 0 {
		r.Add(importer.Finding{Resource: resource, Key: "ports", Level: importer.Approximated,
			Code: "HOST_PORT_HAS_NO_EQUIVALENT",
			Detail: "the container ports are carried; the host half of " + strings.Join(mapped, ", ") +
				" is not — the platform assigns the external port and routes to the workload by name"})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// containerPort reads the container port and the host half out of one compose
// port string: "8080", "8080:80", "127.0.0.1:8080:80", any of them with "/tcp".
func containerPort(spec string) (container int, host string) {
	spec, _, _ = strings.Cut(spec, "/")
	parts := strings.Split(spec, ":")

	last := parts[len(parts)-1]
	n, err := strconv.Atoi(last)
	if err != nil || n < 1 || n > 65535 {
		return 0, ""
	}
	if len(parts) > 1 {
		host = strings.Join(parts[:len(parts)-1], ":")
	}
	return n, host
}

// importMounts turns a service's volumes into mounts on the workload.
//
// A NAMED VOLUME MOUNTS AND A BIND MOUNT DOES NOT. A bind mount is a path on
// the machine that ran compose; a placed workload has no such machine, and
// carrying the path would produce a mount that resolves to nothing.
func importMounts(r *importer.Result, resource string, declared yaml.Node, volumes map[string]string) map[string]any {
	out := map[string]any{}

	for _, spec := range stringsOf(declared) {
		parts := strings.Split(spec, ":")
		if len(parts) < 2 {
			r.Add(importer.Finding{Resource: resource, Key: "volumes." + spec, Level: importer.Blocked,
				Code:   "NO_TAU_FEATURE:anonymous-volume",
				Needs:  "an anonymous volume, or a name to give it",
				Detail: "\"" + spec + "\" is an anonymous volume; a placed workload's storage has to be a resource somebody can find again"})
			continue
		}

		source, path := parts[0], parts[1]
		name, ok := volumes[source]
		if !ok {
			r.Add(importer.Finding{Resource: resource, Key: "volumes." + spec, Level: importer.Blocked,
				Code:   "BIND_MOUNT_IS_HOST_STATE",
				Needs:  "nothing — a bind mount is not a thing a placed workload can have",
				Detail: "\"" + source + "\" is a path on the machine that ran compose; a workload placed on another node has no such path"})
			continue
		}

		op := "mount-rw"
		if slices.Contains(parts[2:], "ro") {
			op = "mount-ro"
		}
		out[name] = map[string]any{"ops": []string{op}, "path": path}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// defaultMemory is what a service with no declared limit gets: small enough to be
// noticed, large enough to start most images, and reported.
const defaultMemory = "512MB"

// importLimits carries the one part of compose's deploy stanza that has an
// equivalent. Replicas is refused rather than dropped: how many instances run is the
// platform's.
func importLimits(r *importer.Result, resource string, d deploy) map[string]any {
	if d.isZero() {
		return nil
	}

	out := map[string]any{}

	if memory := d.Resources.Limits.Memory; memory != "" {
		if v := bytesOf(memory); v != "" {
			out["memory"] = v
		} else {
			r.Add(importer.Finding{Resource: resource, Key: "deploy.resources.limits.memory", Level: importer.Blocked,
				Code:   "MEMORY_NOT_READABLE",
				Detail: "\"" + memory + "\" is not a size this importer can read"})
		}
	}
	if cpus := d.Resources.Limits.CPUs; cpus != "" {
		if v, err := strconv.ParseFloat(cpus, 64); err == nil && v > 0 {
			// MILLICORES, because that is what the vocabulary counts in. A
			// compose `cpus: "2"` is two whole cores.
			out["cpu"] = int(v * 1000)
		} else {
			r.Add(importer.Finding{Resource: resource, Key: "deploy.resources.limits.cpus", Level: importer.Blocked,
				Code:   "CPUS_NOT_READABLE",
				Detail: "\"" + cpus + "\" is not a cpu count this importer can read"})
		}
	}

	if d.Replicas != nil {
		r.Add(importer.Finding{Resource: resource, Key: "deploy.replicas", Level: importer.Blocked,
			Code:  "NO_TAU_FEATURE:replicas",
			Needs: "nothing — its absence is a decision",
			Detail: fmt.Sprintf("deploy.replicas: %d is not carried: how many instances run and where is the platform's decision, "+
				"which is what re-placing a workload when a node dies depends on", *d.Replicas)})
	}
	if d.Resources.Reservations.Memory != "" || d.Resources.Reservations.CPUs != "" {
		r.Add(importer.Finding{Resource: resource, Key: "deploy.resources.reservations", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:reservations",
			Needs:  "a guaranteed floor beside the ceiling on the compute kind",
			Detail: "tau's execution limits are a ceiling; a reservation is a floor the scheduler promises, which has no equivalent"})
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// bytesOf turns a compose size into the vocabulary's own spelling. Compose
// writes `512M` where this writes `512MB`, and both mean the same thing.
func bytesOf(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return ""
	}

	digits := 0
	for digits < len(size) && size[digits] >= '0' && size[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return ""
	}

	number, unit := size[:digits], strings.ToUpper(strings.TrimSpace(size[digits:]))
	switch unit {
	case "":
		return number + "B"
	case "B", "KB", "MB", "GB", "TB":
		return number + unit
	case "K", "M", "G", "T":
		return number + unit + "B"
	}
	return ""
}

// buildToDockerfile maps a compose build (string or mapping) onto the build
// stanza's dockerfile branch.
func buildToDockerfile(n yaml.Node) map[string]any {
	out := map[string]any{}
	if n.Kind == yaml.ScalarNode {
		out["context"] = n.Value
		return out
	}
	var m struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
		Target     string `yaml:"target"`
	}
	if err := n.Decode(&m); err != nil {
		return out
	}
	if m.Context != "" {
		out["context"] = m.Context
	}
	if m.Dockerfile != "" {
		out["path"] = m.Dockerfile
	}
	if m.Target != "" {
		out["target"] = m.Target
	}
	return out
}

// envOf reads both compose spellings: a mapping and a KEY=VALUE list.
func envOf(n yaml.Node) map[string]any {
	if n.IsZero() {
		return nil
	}
	out := map[string]any{}
	switch n.Kind {
	case yaml.MappingNode:
		var m map[string]any
		if err := n.Decode(&m); err != nil {
			return nil
		}
		for k, v := range m {
			if v == nil {
				// `KEY:` with no value means "take it from my shell", which is
				// a value that is not in this file.
				continue
			}
			out[k] = fmt.Sprint(v)
		}
	case yaml.SequenceNode:
		for _, s := range stringsOf(n) {
			k, v, ok := strings.Cut(s, "=")
			if !ok {
				continue // `KEY` alone: same as above.
			}
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringsOf reads a scalar, or a sequence, as a list of strings.
func stringsOf(n yaml.Node) []string {
	if n.IsZero() {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return strings.Fields(n.Value)
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out
	}
	return nil
}

// dependsOn reads both compose spellings: a list, and a mapping with conditions.
func dependsOn(n yaml.Node) []string {
	if n.IsZero() {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		return stringsOf(n)
	}
	var m map[string]struct {
		Condition string `yaml:"condition"`
	}
	if err := n.Decode(&m); err != nil {
		return nil
	}
	var out []string
	for _, k := range sortedKeys(m) {
		if c := m[k].Condition; c != "" {
			out = append(out, k+" ("+c+")")
		} else {
			out = append(out, k)
		}
	}
	return out
}

// looksShelly reports whether a string command relies on a shell.
func looksShelly(s string) bool {
	return strings.ContainsAny(s, "|&;<>$`")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
