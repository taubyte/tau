package jobs

import (
	"fmt"
	"strconv"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

// checkAccountPlan validates the project's `clouds.<NetworkFqdn>.{account, plan}`
// binding against the accounts service. No-op when the accounts client isn't
// wired or the project doesn't pin this cloud. Shared by code.handle() and
// config.handle() so both compile paths enforce the same rule.
func (c Context) checkAccountPlan(p projectSchema.Project) error {
	if c.Accounts == nil {
		return nil
	}
	if c.NetworkFqdn == "" {
		fmt.Fprintf(c.LogFile, "[accounts] warning: monkey did not propagate network FQDN; skipping plan check\n")
		return nil
	}
	binding, ok := p.Get().CloudBinding(c.NetworkFqdn)
	if !ok {
		fmt.Fprintf(c.LogFile, "[accounts] info: project does not declare a binding for %q; skipping plan check\n", c.NetworkFqdn)
		return nil
	}
	if binding.Account == "" || binding.Plan == "" {
		return fmt.Errorf("project config: clouds[%q].{account, plan} must both be set or the entry must be omitted (got account=%q plan=%q)",
			c.NetworkFqdn, binding.Account, binding.Plan)
	}
	provider, externalID := c.gitProviderIdentity()
	if provider == "" || externalID == "" {
		return fmt.Errorf("project config: cannot resolve plan %q without a git provider identity", binding.Plan)
	}
	resp, err := c.Accounts.ResolvePlan(c.ctx, binding.Account, binding.Plan, provider, externalID)
	if err != nil {
		return fmt.Errorf("accounts.ResolvePlan(%s/%s, %s/%s): %w", binding.Account, binding.Plan, provider, externalID, err)
	}
	if !resp.Valid {
		return fmt.Errorf("project config: plan %q under account %q on cloud %q is invalid: %s",
			binding.Plan, binding.Account, c.NetworkFqdn, resp.Reason)
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
