// Package public is the two public forges, bound as a cloud binds them when its
// operator named none. `tau link` installs a webhook whose wire secret is
// derived under the driver's scheme, so the CLI must sign with exactly the
// scheme the cloud verifies with; node's test holds drivers.Default to the
// same schemes.
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
