package compile

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	dream.RegisterFixture("buildLocalProject", Build)
}

type buildFixtureValues struct {
	config   bool
	code     bool
	path     string
	branches []string
}

func (v *buildFixtureValues) parse(params []interface{}) error {
	var err error
	doConfig, ok := params[0].(string)
	v.config, err = strconv.ParseBool(doConfig)
	if !ok {
		return fmt.Errorf("config(bool) is required: %s", err)
	}
	doCode, ok := params[1].(string)
	v.code, err = strconv.ParseBool(doCode)
	if !ok || err != nil {
		return fmt.Errorf("code(bool) is required: %s", err)
	}
	v.path, ok = params[2].(string)
	if !ok {
		return errors.New("path(string) is required")
	}
	v.branches = spec.DefaultBranches

	return nil
}

/*
Example

	dream inject build \
		--config true \
		--code true \
		--branch master \
		--path '/home/sam/projects/P2PProject`
*/
func Build(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}
	err = simple.Provides("tns", "hoarder")
	if err != nil {
		return err
	}
	err = u.Provides(
		"hoarder",
		"tns",
		"monkey",
		"patrick",
	)
	if err != nil {
		return err
	}
	values := &buildFixtureValues{}
	if err := values.parse(params); err != nil {
		return err
	}

	return err
}
