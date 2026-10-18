package worker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// unserved.go is resolve's arm for what a workload declares ABOUT ITSELF.
//
// # Carried, or refused by name
//
// reachesOf decides per target, and degrades per target: a grant this cloud
// cannot serve is refused and the workload still runs. A workload's OWN
// declarations have nothing to degrade to. A ceiling on its CPU, a check of
// whether it is alive, a registry credential to pull it with — served without
// them, the workload is a different deployment from the one written, and the
// author finds out from a bill, a wedged instance or a pull that failed for a
// reason nobody wrote down.
//
// So each one is either CARRIED onto the spec, where a tier applies it, or the
// resolve fails naming it. Nothing in between: a value this file reads and then
// drops is the defect the vocabulary's consumed test exists to catch.
//
// # A default is never a refusal
//
// The compiler materialises defaults — `liveness/failures` is 3 on every
// application whether anybody wrote a liveness block or not. Every test below
// is on a value the AUTHOR wrote: a pointer that is non-nil only when the
// object carries it and no default supplies one, a string the compiler never
// fills, or a block's Declared, which is false for a block of defaults alone.

// ErrUnserved is a workload declaring something about itself that this cloud
// does not do. The message names the attribute and what to change.
var ErrUnserved = errors.New("worker: the workload declares something this cloud does not do")

// applied carries what a tier honours and refuses what none does.
//
// IT RUNS AFTER the spec is otherwise complete, so the runtime it judges by is
// the one the spec will actually be handed to.
func applied(spec *invoke.Spec, t Target, proj *specs.Project, r specs.Resource, resourceID string) error {
	var refused []string

	workload := specs.ReadWorkloadBlock(r)

	// A CPU CEILING IS A CFS QUOTA, and only a container has a cgroup to put
	// one on. A wasm guest is metered in fuel and memory pages; a millicore
	// figure has no counterpart there, so it is refused rather than read as
	// "fast enough".
	if cpu := workload.ExecutionCpu; cpu != nil {
		if spec.Runtime == runtimeContainer {
			spec.CPU = *cpu
		} else {
			refused = append(refused, fmt.Sprintf("execution.cpu is %d millicores, and the %s tier has no CPU ceiling to apply it with — it runs on the container tier only",
				*cpu, spec.Runtime))
		}
	}

	// A CEILING ON INSTANCES IS A CLOUD-WIDE COUNT, and the pool that bounds
	// instances bounds them per node. Applied per node it would allow the
	// ceiling times the fleet, which is the opposite of what a ceiling is for.
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

	// A DELEGATED SERVING BLOCK IS THE COMPILE'S TO READ, and the compiled
	// object records every one it did. One it did not read means this resource
	// runs with what the config repository says instead — the compile reported
	// it, and running it anyway would make that report a footnote.
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

	// READINESS OVER HTTP IS WHAT THE CONTAINER TIER ALREADY WAITS ON, made
	// specific: the same wait, asking the path the author named on the port
	// they named rather than settling for an accepted connection.
	readiness := app.Readiness
	switch readiness.Kind {
	case specs.ApplicationReadinessKindHttp:
		spec.Readiness = &invoke.Probe{Path: readiness.HttpPath, Port: readiness.HttpPort}
	case specs.ApplicationReadinessKindFunction:
		refused = append(refused, fmt.Sprintf("readiness asks the function %s%s, and nothing here calls a function to decide whether an instance may take work",
			readiness.FunctionName, inNamespace(readiness.FunctionNamespace)))
	}

	// NOTHING WATCHES A RUNNING INSTANCE. Readiness gates the start; after it,
	// an instance is replaced when it exits and not when it stops answering —
	// so a liveness check would be a promise to restart a wedged workload that
	// nothing keeps. `failures` is defaulted, so Declared is what says a block
	// was written.
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
