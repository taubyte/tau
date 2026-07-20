package jobs

import (
	"fmt"
	"strconv"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

// checkAccountPlan validates the project's clouds.<NetworkFqdn> binding against
// the accounts service. Monkey is build-agnostic here: it hands the whole
// binding to accounts.Validate and lets the accounts client decide what to
// check (the community build checks linkage; richer builds read more from the
// binding). No-op when the accounts client isn't wired or the project doesn't
// pin this cloud. Shared by code.handle() and config.handle().
func (c Context) checkAccountPlan(p projectSchema.Project) error {
	if c.Accounts == nil {
		return nil
	}
	if c.NetworkFqdn == "" {
		fmt.Fprintf(c.LogFile, "[accounts] warning: monkey did not propagate network FQDN; skipping account check\n")
		return nil
	}
	binding, ok := p.Get().CloudBinding(c.NetworkFqdn)
	if !ok {
		fmt.Fprintf(c.LogFile, "[accounts] info: project does not declare a binding for %q; skipping account check\n", c.NetworkFqdn)
		return nil
	}
	if binding.Account == "" {
		return fmt.Errorf("project config: clouds[%q].account must be set or the entry must be omitted", c.NetworkFqdn)
	}
	provider, externalID := c.gitProviderIdentity()
	if provider == "" || externalID == "" {
		return fmt.Errorf("project config: cannot validate account %q without a git provider identity", binding.Account)
	}
	resp, err := c.Accounts.Validate(c.ctx, provider, externalID, binding)
	if err != nil {
		return fmt.Errorf("accounts.Validate(%s, %s/%s): %w", binding.Account, provider, externalID, err)
	}
	if !resp.Valid {
		return fmt.Errorf("project config: cloud binding for account %q on cloud %q is invalid: %s",
			binding.Account, c.NetworkFqdn, resp.Reason)
	}
	return nil
}

// gitProviderIdentity reads (provider, external_id) from the patrick Job's
// repository metadata. Defaults provider to "github" — the only wired one
// today; extend Job.Meta.Repository.Provider when others land.
func (c Context) gitProviderIdentity() (provider, externalID string) {
	if c.Job == nil {
		return "", ""
	}
	repo := c.Job.Meta.Repository
	provider = "github"
	if repo.Provider != "" {
		provider = repo.Provider
	}
	if repo.ID != 0 {
		externalID = strconv.Itoa(repo.ID)
	}
	return provider, externalID
}
