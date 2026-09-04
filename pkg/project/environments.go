package project

import (
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// environments.go answers "which deployments does this branch feed" from what
// the register published, with no trip back to git.
//
// THAT IS WHAT MAKES A CODE PUSH BUILDABLE. A push to a code repository knows a
// project and a branch and nothing else; everything it needs to decide what to
// build for is already in the object the config repository's deploy left
// behind, because the environment table survives the compile.

// Environments is the deployment table this project declares, sorted by name,
// each parent's shadows nested inside it.
//
// A project that declares none has exactly one deployment and no name for it
// here, so this is empty — inventing one would be this package naming a
// deployment the author did not.
func (p *Project) Environments() []schema.Environment {
	p.environmentsOnce.Do(func() { p.environments = schema.EnvironmentsIn(p.root) })
	return p.environments
}

// Deployments is every environment a pushed branch feeds, shadows included.
//
// ONE BRANCH FEEDS SEVERAL: `main` feeds production AND the shadow written
// inside it, because a shadow is its own release over its parent's state. A
// caller that took the first match would silently stop building one of them.
func (p *Project) Deployments(branch string) []string {
	return schema.DeploymentNames(p.Environments(), branch)
}
