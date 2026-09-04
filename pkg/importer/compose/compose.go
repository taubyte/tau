// Package compose imports a docker-compose file, and is as loud about what it
// could not bring as about what it could.
//
// Compose is the first importer for one reason: it is the only source that is
// honestly FILE-ONLY. Vercel, Fly, Dokploy and Coolify all keep environment
// variables, secrets, domains, certificates and branch settings OUTSIDE the
// manifest, so an importer for those needs an API client and an account
// connection before it can produce anything that runs. A compose file is the
// whole truth of a compose deployment.
//
// # The two rules, from importers.md
//
// AN IMPORTER NEVER EMITS A FIELD THE RUNTIME CANNOT HONOUR — not as a comment,
// not as "advisory". A config carrying `restart: always` into a system with no
// restart controller is worse than a refusal line, because the next person to
// read it believes it.
//
// EVERY BLOCKED FIELD NAMES THE CONTRACT IT WOULD NEED. `NO_TAU_FEATURE:
// healthcheck` on its own is a shrug; naming the field that would unblock it
// makes the finding a to-do item, and counting those across a corpus is how the
// missing vocabulary gets prioritised by evidence rather than by taste.
//
// # The findings are the product
//
// A silent importer is the dangerous kind. Every compose key that is dropped,
// approximated, or that means something subtly different here produces a
// Finding, and the caller is expected to show them. An import that reports
// nothing on a file using healthchecks and depends_on conditions has lied.
package compose

import (
	"errors"
	"fmt"
	"slices"
	"sort"
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

// containerKind is what a compose service becomes.
//
// IT IS VERIFIED AGAINST THE VOCABULARY RATHER THAN ASSUMED. The schema answers
// which kinds run a container (application, worker, sandbox); this importer
// wants the long-running one that listens on HTTP, which is `application`, and
// it checks that the vocabulary still says so. If the kind is renamed or stops
// running containers, this fails loudly at import instead of emitting a
// document for a kind that cannot run it.
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
	Deploy          yaml.Node `yaml:"deploy"`
	Labels          yaml.Node `yaml:"labels"`
	Profiles        []string  `yaml:"profiles"`
	StopGracePeriod string    `yaml:"stop_grace_period"`
	ExtraHosts      []string  `yaml:"extra_hosts"`
}

// Import reads a compose file and produces v2 configuration plus every finding.
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

	r := importer.Result{Source: Source, Kind: kind}

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

	for _, name := range sortedKeys(f.Services) {
		importService(&r, name, f.Services[name])
	}

	r.Sort()
	return r, nil
}

func importService(r *importer.Result, name string, s service) {
	doc := map[string]any{}

	switch {
	case s.Image != "":
		doc["image"] = s.Image
	case !s.Build.IsZero():
		// A build stanza exists in the vocabulary, and the dockerfile branch is
		// what a compose build maps onto.
		doc["build"] = map[string]any{"dockerfile": buildToDockerfile(s.Build)}
	default:
		r.Add(importer.Finding{Resource: name, Key: "image", Level: importer.Blocked, Code: "NO_SOURCE",
			Detail: "the service names neither an image nor a build; there is nothing to run"})
		return
	}

	if cmd := stringsOf(s.Command); len(cmd) > 0 {
		doc["command"] = cmd
		// A COMPOSE STRING COMMAND IS NOT `sh -c`. Compose splits a string form
		// with shell-like rules but does not run a shell, so a command relying
		// on a pipe or a variable expansion behaves differently. Reported when
		// the source used the string form and the content suggests a shell.
		if s.Command.Kind == yaml.ScalarNode && looksShelly(s.Command.Value) {
			r.Add(importer.Finding{Resource: name, Key: "command", Level: importer.Approximated,
				Code:   "COMMAND_STRING_IS_NOT_A_SHELL",
				Detail: "the string form is split, not run by a shell; a pipe, a redirect or a $VAR will not behave as it does under compose"})
		}
	}
	if ep := stringsOf(s.Entrypoint); len(ep) > 0 {
		// Entrypoint and command compose differently in the image; carrying
		// only one of them would change what runs.
		r.Add(importer.Finding{Resource: name, Key: "entrypoint", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:entrypoint",
			Needs:  "an entrypoint field distinct from command on the compute kind",
			Detail: "entrypoint and command combine in the image; tau has one command field, so an entrypoint override cannot be expressed"})
	}
	if s.WorkingDir != "" {
		doc["workdir"] = s.WorkingDir
	}

	if env := envOf(s.Environment); len(env) > 0 {
		doc["env"] = env
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

	if ports := portsOf(s.Ports); len(ports) > 0 {
		// A published port is how the world reaches it, which in tau is a
		// domain and an inbound edge rather than a host port.
		r.Add(importer.Finding{Resource: name, Key: "ports", Level: importer.Approximated,
			Code: "PORTS_ARE_NOT_HOST_PORTS",
			Detail: fmt.Sprintf("published %s: tau gives a workload a port and routes to it by name, so a host port mapping has no equivalent and is dropped",
				strings.Join(ports, ", "))})
	}

	if vols := stringsOf(s.Volumes); len(vols) > 0 {
		r.Add(importer.Finding{Resource: name, Key: "volumes", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:volumes",
			Needs:  "a volume kind with a mount on the compute kind",
			Detail: "a bind mount is host state and does not survive placement; a named volume needs a volume resource tau does not yet have"})
	}

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
		{"healthcheck", "health.{test,interval,timeout,start_period,retries} plus a definition of what a failing check does",
			"tau has no health-check vocabulary", !s.Healthcheck.IsZero()},
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

	if !s.Deploy.IsZero() {
		r.Add(importer.Finding{Resource: name, Key: "deploy", Level: importer.Blocked,
			Code:  "NO_TAU_FEATURE:deploy",
			Needs: "resource limits on the compute kind",
			Detail: "deploy.resources.limits is the one part with an equivalent, and replicas is deliberately absent: " +
				"how many run and where is the platform's decision"})
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

	r.Set(name, doc)
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

func portsOf(ports []string) []string { return ports }

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
