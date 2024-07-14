package domains

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func FQDN(value string) basic.Op {
	return basic.Set("fqdn", value)
}

func UseCertificate(value bool) basic.Op {
	return func(ci basic.ConfigIface) []*seer.Query {
		var val struct{}
		if value && ci.Config().Get("certificate").Value(val) != nil {
			return basic.Set("certificate", nil)(ci)
		}

		return []*seer.Query{ci.Config().Get("certificate").Delete()}
	}
}

func Type(value string) basic.Op {
	return basic.SetChild("certificate", "type", value)
}

func Cert(value string) basic.Op {
	return basic.SetChild("certificate", "cert", value)
}

func Key(value string) basic.Op {
	return basic.SetChild("certificate", "key", value)
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
