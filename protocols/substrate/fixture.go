package substrate

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/taubyte/tau/libdream/common"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
	orbit "github.com/taubyte/vm-orbit/plugin/vm"

	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/tns"
)

func init() {
	dreamlandRegistry.Fixture("attachDomain", pushDomain)
	dreamlandRegistry.Fixture("attachPlugin", injectPlugin)
}

func pushDomain(u common.Universe, params ...interface{}) error {
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

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	err = commonTest.RegisterTestDomain(u.Context(), mockAuthURL)
	if err != nil {
		return err
	}

	return nil
}

func injectPlugin(u common.Universe, params ...interface{}) error {
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
