// Package public is the two public forges, bound the way a cloud binds them
// when its operator named none.
//
// IT EXISTS FOR THE DEVELOPER'S SIDE OF A LINK. `tau link` installs a webhook
// with the developer's token, and the wire secret it installs is derived under
// the forge driver's signing scheme — so the CLI's driver for a host must sign
// with exactly the scheme the cloud verifies with, or every delivery fails.
// node.DefaultForges is the cloud's half of that agreement, and node's own test
// holds the two to the same schemes host by host.
package public

import (
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
	"github.com/taubyte/tau/pkg/forge/gitlab"
)

// Registry is github.com and gitlab.com, gitlab.com signing its hooks with
// Standard Webhooks.
func Registry(policy forge.Policy) (*forge.Registry, error) {
	gh, err := github.New("github.com")
	if err != nil {
		return nil, err
	}
	gl, err := gitlab.New("gitlab.com", gitlab.Signing())
	if err != nil {
		return nil, err
	}
	return forge.New(policy,
		forge.Binding{Host: "github.com", Driver: gh},
		forge.Binding{Host: "gitlab.com", Driver: gl})
}
