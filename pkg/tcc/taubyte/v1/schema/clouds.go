package schema

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// FlattenClouds promotes the project's `clouds.<fqdn>.{account, plan}` entry for
// the compile-target cloud into flat `account` / `plan` scalars at the project
// root, then drops the entire `clouds` map. Empty fqdn (dream / non-cloud-aware
// tooling) drops the map without promotion. A partial entry (one half set) fails
// compile so `tau validate config` flags the bad shape too. Ported verbatim from
// the old pass1/cloud.go (c.fqdn -> tc.Cloud), wired onto cloudsGroup() below via
// interp.GroupTransform. It lives in schema (not interp) because it is a piece of
// the DSL's cloud declaration, and interp.TC is the runtime carrier the driver
// hands it — schema imports interp, so it names interp.TC directly.
func FlattenClouds(tc *interp.TC, o object.Object[object.Refrence]) error {
	cloudsObj, err := o.Child("clouds").Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading clouds map failed with %w", err)
	}
	defer o.Delete("clouds")

	if tc.Cloud == "" {
		return nil
	}

	entryObj, err := cloudsObj.Child(tc.Cloud).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading clouds[%q] failed with %w", tc.Cloud, err)
	}

	account, _ := entryObj.GetString("account")
	plan, _ := entryObj.GetString("plan")

	if (account == "") != (plan == "") {
		return fmt.Errorf(
			"project config: clouds[%q] is incomplete; both `account` and `plan` must be set or the entry must be omitted (got account=%q plan=%q)",
			tc.Cloud, account, plan,
		)
	}
	if account == "" {
		return nil
	}

	o.Set("account", account)
	o.Set("plan", plan)
	return nil
}
