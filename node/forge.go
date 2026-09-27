package node

import (
	"fmt"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
	"github.com/taubyte/tau/pkg/forge/gitlab"
)

// forge.go says which git hosts this cloud serves, and which driver speaks for
// each.
//
// THE HOST IS THE PROVIDER, and a binding is the one place that has to say more
// than the host: gitlab.example.com could be a GitLab or a Gitea, and nothing
// about the name tells them apart. So a deployment names the DRIVER per host,
// here, once — and everything downstream asks the registry by host and never
// learns there was a choice.

// Forge is one git host this cloud serves.
type Forge struct {
	// Host is the host as it appears in a clone URL: "github.com",
	// "gitlab.example.com", "git.internal:8443".
	Host string

	// Driver is the API the host speaks: "github" for github.com and GitHub
	// Enterprise, "gitlab" for GitLab.
	Driver string

	// Signing registers a GitLab instance's hooks with Standard Webhooks, which
	// binds the body, rather than a replayed shared token. An instance too old
	// to know it signs nothing and every delivery fails verification — closed,
	// and visibly broken. Meaningless for a github driver, which always signs.
	Signing bool
}

// The drivers a Forge may name.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
)

// DefaultForges is what a node serves when its operator named none: the two
// public forges. A self-hosted instance is an operator's to add.
var DefaultForges = []Forge{
	{Host: "github.com", Driver: ForgeGitHub},
	{Host: "gitlab.com", Driver: ForgeGitLab, Signing: true},
}

// forges builds the registry every provider question in this node is asked
// through. The zero Policy is the strict one: an instance that can only offer
// an unbound scheme is refused here, at startup, rather than accepted quietly.
func forges(declared []Forge) (*forge.Registry, error) {
	if len(declared) == 0 {
		declared = DefaultForges
	}

	bindings := make([]forge.Binding, 0, len(declared))
	for _, f := range declared {
		var (
			d   forge.Driver
			err error
		)
		switch f.Driver {
		case ForgeGitHub:
			d, err = github.New(f.Host)
		case ForgeGitLab:
			var opts []gitlab.Option
			if f.Signing {
				opts = append(opts, gitlab.Signing())
			}
			d, err = gitlab.New(f.Host, opts...)
		default:
			return nil, fmt.Errorf("node: forge %q names driver %q; a driver is one of %q, %q",
				f.Host, f.Driver, ForgeGitHub, ForgeGitLab)
		}
		if err != nil {
			return nil, fmt.Errorf("node: forge %q: %w", f.Host, err)
		}
		bindings = append(bindings, forge.Binding{Host: f.Host, Driver: d})
	}
	return forge.New(forge.Policy{}, bindings...)
}
