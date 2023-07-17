package service

import (
	"time"

	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	"bitbucket.org/taubyte/dreamland/common"
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
)

func init() {
	dreamlandRegistry.Fixture("createProjectWithCustomer", fixture)
}

func fixture(u common.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return err
	}

	err = simple.Provides("billing", "auth")
	if err != nil {
		return err
	}

	err = u.Provides(
		"auth",
		"billing",
		"tns",
	)
	if err != nil {
		return err
	}

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	err = commonTest.RegisterTestProject(u.Context(), mockAuthURL)
	if err != nil {
		return err
	}

	time.Sleep(10 * time.Second)

	ids, err := simple.Auth().Projects().List()
	if err != nil {
		return err
	}

	// FIXME: might cause a problem if there are more than one project registered
	_, err = simple.Billing().New(ids[0])
	if err != nil {
		return err
	}

	return nil
}
