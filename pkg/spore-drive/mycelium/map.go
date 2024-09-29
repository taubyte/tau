package mycelium

import (
	"fmt"

	"github.com/taubyte/tau/pkg/mycelium"
	auth "github.com/taubyte/tau/pkg/mycelium/auth"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func Map(parser config.Parser) (network *mycelium.Network, err error) {
	if network, err = mycelium.New(); err != nil {
		return
	}

	var hosts []host.Host
	for _, h := range parser.Hosts().List() {
		hp := parser.Hosts().Host(h)
		var mhauth []*auth.Auth
		for _, a := range hp.SSH().Auth().List() {
			ap := parser.Auth().Get(a)
			if p := ap.Password(); p != "" {
				if a, err := auth.New(ap.Username(), auth.Password(p)); err == nil {
					mhauth = append(mhauth, a)
				} else {
					return nil, err // Add details to error
				}
			} else if key, err := ap.Open(); err == nil {
				if a, err := auth.New(ap.Username(), auth.Key(key)); err == nil {
					mhauth = append(mhauth, a)
				} else {
					return nil, err // Add details to error
				}
			} else {
				return nil, fmt.Errorf("failed to parse auth %s: no password and no key", a)
			}
		}

		var tags []string
		for _, sp := range hp.Shapes().List() {
			tags = append(tags, fmt.Sprintf("shape[%s]", sp))
		}

		mh, err := host.New(
			host.Name(h),
			host.Address(hp.SSH().Address()),
			host.Port(hp.SSH().Port()),
			host.Auths(mhauth...),
			host.Tags(tags...),
		)
		if err != nil {
			return nil, err // Add details to error
		}

		hosts = append(hosts, mh)
	}

	return network, network.Add(hosts...)
}
