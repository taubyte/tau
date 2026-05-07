package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// Cloud promotes the project's `clouds.<fqdn>.{account, plan}` entry for the
// compile-target cloud into flat `account` / `plan` scalars at the project
// root, then drops the entire `clouds` map. Empty fqdn (dream / non-cloud-
// aware tooling) drops the map without promotion. Partial entries (one half
// set) fail compile so `tau validate config` flags the bad shape too.
type cloud struct {
	fqdn string
}

func Cloud(fqdn string) transform.Transformer[object.Refrence] {
	return &cloud{fqdn: fqdn}
}

func (c *cloud) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	cloudsObj, err := o.Child("clouds").Object()
	if err == object.ErrNotExist {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading clouds map failed with %w", err)
	}
	defer o.Delete("clouds")

	if c.fqdn == "" {
		return o, nil
	}

	entryObj, err := cloudsObj.Child(c.fqdn).Object()
	if err == object.ErrNotExist {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading clouds[%q] failed with %w", c.fqdn, err)
	}

	account, _ := entryObj.GetString("account")
	plan, _ := entryObj.GetString("plan")

	if (account == "") != (plan == "") {
		return nil, fmt.Errorf(
			"project config: clouds[%q] is incomplete; both `account` and `plan` must be set or the entry must be omitted (got account=%q plan=%q)",
			c.fqdn, account, plan,
		)
	}
	if account == "" {
		return o, nil
	}

	o.Set("account", account)
	o.Set("plan", plan)
	return o, nil
}
