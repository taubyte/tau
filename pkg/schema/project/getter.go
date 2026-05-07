package project

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*project
}

func (p *project) Get() Getter {
	return &getter{p}
}

func (g getter) Id() string {
	return basic.Get[string](g, "id")
}

func (g getter) Name() string {
	return basic.Get[string](g, "name")
}

func (g getter) Description() string {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() []string {
	return basic.Get[[]string](g, "tags")
}

func (g getter) Email() string {
	return basic.Get[string](g, "notification", "email")
}

// CloudBinding is the (account, plan) pair a project pins to on a specific
// tau cloud (identified by FQDN).
type CloudBinding struct {
	Account string `json:"account,omitempty" yaml:"account,omitempty"`
	Plan    string `json:"plan,omitempty"    yaml:"plan,omitempty"`
}

func (b CloudBinding) IsEmpty() bool {
	return b.Account == "" && b.Plan == ""
}

// CloudBinding reads `clouds.<fqdn>.{account, plan}`. The bool reports
// whether the entry exists (false = dream/local or no entry for this cloud).
func (g getter) CloudBinding(fqdn string) (CloudBinding, bool) {
	if fqdn == "" {
		return CloudBinding{}, false
	}
	acc := basic.Get[string](g, "clouds", fqdn, "account")
	plan := basic.Get[string](g, "clouds", fqdn, "plan")
	if acc == "" && plan == "" {
		return CloudBinding{}, false
	}
	return CloudBinding{Account: acc, Plan: plan}, true
}

// Clouds lists the FQDNs the project declares bindings for. Reads the raw
// map directly because `clouds` is free-form (not a schema-registered group
// like `applications`), so seer.List() doesn't apply.
func (g getter) Clouds() []string {
	raw := basic.Get[map[string]any](g, "clouds")
	if len(raw) == 0 {
		altRaw := basic.Get[map[any]any](g, "clouds")
		out := make([]string, 0, len(altRaw))
		for k := range altRaw {
			if s, ok := k.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	out := make([]string, 0, len(raw))
	for k := range raw {
		out = append(out, k)
	}
	return out
}

func (g getter) Applications() []string {
	apps, err := g.seer.Get("applications").List()
	if err != nil {
		return nil
	}

	return apps
}
