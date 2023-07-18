package compile

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/taubyte/dreamland/core/common"
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	spec "github.com/taubyte/go-specs/common"
)

func init() {
	dreamlandRegistry.Fixture("buildLocalProject", Build)
}

type buildFixtureValues struct {
	config bool
	code   bool
	path   string
	branch string
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
	v.branch = params[3].(string)
	if v.branch == "" {
		v.branch = spec.DefaultBranch
	}

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
func Build(u common.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}
	err = simple.Provides("tns")
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
	fmt.Println(values)
	return err
}
