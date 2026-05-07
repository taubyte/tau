package project

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Name(value string) basic.Op {
	return basic.Set("name", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Email(value string) basic.Op {
	return basic.SetChild("notification", "email", value)
}

// CloudBindingOp sets `clouds.<fqdn>.{account, plan}` in one call. Each
// child uses an independent c.Config() chain because yaseer's Query.Get()
// mutates the receiver.
func CloudBindingOp(fqdn, account, plan string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		return []*seer.Query{
			c.Config().Get("clouds").Get(fqdn).Get("account").Set(account),
			c.Config().Get("clouds").Get(fqdn).Get("plan").Set(plan),
		}
	}
}

func CloudAccount(fqdn, account string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		return []*seer.Query{c.Config().Get("clouds").Get(fqdn).Get("account").Set(account)}
	}
}

func CloudPlan(fqdn, plan string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		return []*seer.Query{c.Config().Get("clouds").Get(fqdn).Get("plan").Set(plan)}
	}
}
