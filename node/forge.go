package node

import (
	"errors"
	"fmt"

	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gitea"
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
	// Enterprise, "gitlab" for GitLab, "gitea" for Gitea, Forgejo and
	// Codeberg, which share one API.
	Driver string

	// HostKeys are the ssh host keys this forge may answer a clone with, one per
	// line as `ssh-keyscan` or a forge's own docs print them ("[host] type
	// base64"). A build clones over ssh with the repository's deploy key, and a
	// clone that accepted any host key could be answered by anyone on the path,
	// so a forge with none is not cloned from. github.com and gitlab.com default
	// to their published keys; any other host must name its own.
	HostKeys []string

	// Signing registers a GitLab instance's hooks with Standard Webhooks, which
	// binds the body, rather than a replayed shared token. An instance too old
	// to know it signs nothing and every delivery fails verification — closed,
	// and visibly broken. Meaningless for a github or gitea driver, which
	// always signs.
	Signing bool
}

// The drivers a Forge may name.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
	ForgeGitea  = "gitea"
)

// DefaultForges is what a node serves when its operator named none: the two
// public forges. A self-hosted instance is an operator's to add.
var DefaultForges = []Forge{
	{Host: "github.com", Driver: ForgeGitHub},
	{Host: "gitlab.com", Driver: ForgeGitLab, Signing: true},
}

// knownHostKeys are the published ssh host keys of the two public forges,
// taken from each forge's own HTTPS source (api.github.com/meta,
// docs.gitlab.com/user/gitlab_com) and checked against the fingerprints each
// publishes separately. A forge that rotates its keys rotates them here.
var knownHostKeys = map[string][]string{
	"github.com": {
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=",
	},
	"gitlab.com": {
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Yt0aGROY5qX2UKSeOvuP4D6TPqKF1onrSzH9bx9XUf2lEdWT/ia1NEKjunUqu1xOB/StKDHMoX4/OKyIzuS0q/T1zOATthvasJFoPrAjkohTyaDUz2LN5JoH839hViyEG82yB+MjcFV5MU3N1l1QL3cVUCh93xSaua1N85qivl+siMkPGbO5xR/En4iEY6K2XPASUEMaieWVNTRCtJ4S8H+9",
	},
}

// hostKeys parses every forge's host keys, falling back to the published ones
// for a public forge that named none. A key that does not parse fails the boot:
// a typo there is a forge nothing can clone from, found at the first push.
func hostKeys(declared []Forge) (map[string][]gossh.PublicKey, error) {
	if len(declared) == 0 {
		declared = DefaultForges
	}
	out := make(map[string][]gossh.PublicKey, len(declared))
	for _, f := range declared {
		lines := f.HostKeys
		if len(lines) == 0 {
			lines = knownHostKeys[f.Host]
		}
		for _, line := range lines {
			key, err := parseHostKey(line)
			if err != nil {
				return nil, fmt.Errorf("node: forge %q host key %q: %w", f.Host, line, err)
			}
			out[f.Host] = append(out[f.Host], key)
		}
	}
	return out, nil
}

// parseHostKey reads one key as a known_hosts line ("host type base64") or a
// bare public key ("type base64").
func parseHostKey(line string) (gossh.PublicKey, error) {
	if _, _, key, _, _, err := gossh.ParseKnownHosts([]byte(line)); err == nil {
		return key, nil
	}
	key, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
	return key, err
}

// forges builds the registry every provider question in this node is asked
// through. The zero Policy is the strict one: an instance that can only offer
// an unbound scheme is refused here, at startup, rather than accepted quietly.
// WithForges replaces the forges the config declares with a registry built
// elsewhere — an in-memory forge in a test. Production binds forges from config.
func WithForges(r *forge.Registry) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: a nil forge registry")
		}
		o.forges = r
		return nil
	}
}

// forgeRegistry is the injected registry, or the one the config declares.
func (n *Node) forgeRegistry() (*forge.Registry, error) {
	if n.opts.forges != nil {
		return n.opts.forges, nil
	}
	return forges(n.cfg.Forges)
}

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
		case ForgeGitea:
			d, err = gitea.New(f.Host)
		default:
			return nil, fmt.Errorf("node: forge %q names driver %q; a driver is one of %q, %q, %q",
				f.Host, f.Driver, ForgeGitHub, ForgeGitLab, ForgeGitea)
		}
		if err != nil {
			return nil, fmt.Errorf("node: forge %q: %w", f.Host, err)
		}
		bindings = append(bindings, forge.Binding{Host: f.Host, Driver: d})
	}
	return forge.New(forge.Policy{}, bindings...)
}
