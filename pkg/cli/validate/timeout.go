package validate

import (
	"github.com/taubyte/tau/pkg/cli/i18n"
	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
)

func Time(s string) error {
	val, err := schemaCommon.StringToTime(s)
	if err != nil {
		return err
	}

	if val == 0 {
		return i18n.ErrorTime0Invalid()
	}

	return nil
}
