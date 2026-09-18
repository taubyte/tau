//go:build dreaming
// +build dreaming

package substrate

import (
	commonIface "github.com/taubyte/tau/core/common"
	commonTest "github.com/taubyte/tau/dream/helpers"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func init() {
	dream.RegisterFixture("attachDomain", pushDomain)
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
