package substrate

import (
	_ "embed"
	"fmt"
	"strings"

	commonIface "github.com/taubyte/tau/core/common"
	commonTest "github.com/taubyte/tau/dream/helpers"
	orbit "github.com/taubyte/tau/pkg/vm-orbit/satellite/vm"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func init() {
	dream.RegisterFixture("attachDomain", pushDomain)
	dream.RegisterFixture("attachPlugin", injectPlugin)
}

func pushDomain(u *dream.Universe, params ...interface{}) error {
	err := u.Provides(
		"auth",
		"patrick",
		"monkey",
		"hoarder",
		"tns",
	)
	if err != nil {
		return err
	}

	url := commonTest.TestFQDN
	if len(params) > 0 {
		commonTest.TestFQDN = params[0].(string)
	} else {
		commonTest.TestFQDN = "testing_website_builder.com"
	}
	defer func() {
		commonTest.TestFQDN = url
	}()

	// Create a simple to get the auth client
	simple, err := u.Simple("client")
	if err != nil {
		// If simple doesn't exist, create it
		err = u.StartWithConfig(&dream.Config{
			Simples: map[string]dream.SimpleConfig{
				"client": {
					Clients: dream.SimpleConfigClients{
						Auth: &commonIface.ClientConfig{},
					}.Compat(),
				},
			},
		})
		if err != nil {
			return err
		}
		simple, err = u.Simple("client")
		if err != nil {
			return err
		}
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	err = commonTest.RegisterTestDomain(u.Context(), auth)
	if err != nil {
		return err
	}

	return nil
}

func injectPlugin(u *dream.Universe, params ...interface{}) error {
	if err := u.Provides(
		"auth",
		"patrick",
		"monkey",
		"hoarder",
		"tns",
	); err != nil {
		return err
	}

	ctx := u.Context()
	node := u.Substrate()
	srv, ok := node.(*Service)
	if !ok {
		return fmt.Errorf("node service %#v is not type %v", node, srv)
	}

	if len(params) > 0 {
		pluginList, ok := params[0].(string)
		if !ok {
			return fmt.Errorf("expected param 1 to be comma separated string list")
		}

		plugins := strings.SplitAfter(pluginList, ",")

		for _, path := range plugins {
			plugin, err := orbit.Load(path, ctx)
			if err != nil {
				return fmt.Errorf("loading plugin from `%s` failed with: %w", path, err)
			}

			srv.orbitals = append(srv.orbitals, plugin)
		}
	}

	return nil
}
