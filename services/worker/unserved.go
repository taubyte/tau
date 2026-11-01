package worker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// What a workload declares about itself has nothing to degrade to, unlike a grant: it
// is either carried onto the spec for a tier to apply, or the resolve fails naming
// it. A default is never a refusal: every check is on a value the author wrote (a
// non-nil pointer, a string the compiler never fills, or a block's Declared).

// ErrUnserved is a workload declaring something about itself that this cloud
// does not do. The message names the attribute and what to change.
var ErrUnserved = errors.New("worker: the workload declares something this cloud does not do")

// applied carries what a tier honours and refuses what none does. It runs after the
// spec is otherwise complete, so it judges by the real runtime.
func applied(spec *invoke.Spec, t Target, proj *specs.Project, r specs.Resource, resourceID string) error {
	var refused []string

	workload := specs.ReadWorkloadBlock(r)

	// A CPU ceiling is a CFS quota, which only a container has; a wasm guest is metered
	// in fuel and pages.
	if cpu := workload.ExecutionCpu; cpu != nil {
		if spec.Runtime == runtimeContainer {
			spec.CPU = *cpu
		} else {
			refused = append(refused, fmt.Sprintf("execution.cpu is %d millicores, and the %s tier has no CPU ceiling to apply it with — it runs on the container tier only",
				*cpu, spec.Runtime))
		}
	}

	// A ceiling on instances is cloud-wide, and the pool bounds them per node.
	if most := workload.LimitsMaxInstances; most != nil {
		refused = append(refused, fmt.Sprintf("limits.max_instances is %d, and nothing in this cloud counts a workload's instances across nodes — "+
			"each node would run up to its own ceiling", *most))
	}

	if t.Kind == specs.KindApplication {
		refused = append(refused, probes(spec, specs.ReadApplication(r))...)
	}

	if t.Kind == specs.KindSandbox {
		// A SANDBOX LIVES AS LONG AS THE POOL KEEPS IT, which is the node's
		// idle timeout and no per-workload clock at all.
		if lifetime := specs.ReadSandbox(r).Lifetime; lifetime.Idle != nil || lifetime.Max != nil {
			refused = append(refused, fmt.Sprintf("lifetime (idle %s, max %s) is declared, and nothing here stops a sandbox on a clock of its own — "+
				"it is kept or reaped by the node's idle timeout", orNone(lifetime.Idle), orNone(lifetime.Max)))
		}
	}

	source := specs.ReadSourceBlock(r)

	// A PULL CREDENTIAL IS NOT CARRIED TO THE BACKENDS: neither pulls with
	// one. An image on a private registry would fail to pull with an error
	// about authentication that the config had answered.
	if name := source.SourceImagePullSecretName; name != "" {
		refused = append(refused, fmt.Sprintf("source.image_pull_secret names the secret %s%s, and this cloud pulls images anonymously — "+
			"push it to a registry that needs no credential", name, inNamespace(source.SourceImagePullSecretNamespace)))
	}

	// A delegated serving block the compile did not read means the resource would run
	// with the config repository's values instead.
	if config := source.SourceConfig; config != "" && !proj.Filled(resourceID) {
		refused = append(refused, fmt.Sprintf("source.config names %q, and the compile that produced this deployment did not read it — "+
			"what it would run is the config repository's block, not that file's", config))
	}

	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s/%s: %s", ErrUnserved, t.Kind, t.Name, strings.Join(refused, "; "))
}

// probes carries an application's readiness check and refuses the rest.
func probes(spec *invoke.Spec, app specs.Application) []string {
	var refused []string

	// Readiness over HTTP is the container tier's existing wait, asking the named path on
	// the named port.
	readiness := app.Readiness
	switch readiness.Kind {
	case specs.ApplicationReadinessKindHttp:
		spec.Readiness = &invoke.Probe{Path: readiness.HttpPath, Port: readiness.HttpPort}
	case specs.ApplicationReadinessKindFunction:
		refused = append(refused, fmt.Sprintf("readiness asks the function %s%s, and nothing here calls a function to decide whether an instance may take work",
			readiness.FunctionName, inNamespace(readiness.FunctionNamespace)))
	}

	// Nothing watches a running instance after readiness: it is replaced when it exits,
	// not when it stops answering. `failures` is defaulted, so Declared says a block was
	// written.
	if liveness := app.Liveness; liveness.Declared {
		check := fmt.Sprintf("GET %s on port %q", liveness.HttpPath, liveness.HttpPort)
		if liveness.Kind == specs.ApplicationLivenessKindFunction {
			check = fmt.Sprintf("the function %s%s", liveness.FunctionName, inNamespace(liveness.FunctionNamespace))
		}
		failures := int64(0)
		if liveness.Failures != nil {
			failures = *liveness.Failures
		}
		refused = append(refused, fmt.Sprintf("liveness (%s, replaced after %d failures) is declared, and nothing here checks a running instance — "+
			"one that stops answering is kept until it exits", check, failures))
	}
	return refused
}

// runtimeContainer is the tier with a cgroup.
const runtimeContainer = specs.WorkloadBlockRuntimeContainer

func inNamespace(namespace string) string {
	if namespace == "" {
		return ""
	}
	return " in " + namespace
}

func orNone[T fmt.Stringer](v *T) string {
	if v == nil {
		return "none"
	}
	return (*v).String()
}
