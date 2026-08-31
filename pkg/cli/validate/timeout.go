package validate

import (
	schemaCommon "github.com/taubyte/tau/legacy/schema/common"
	"github.com/taubyte/tau/pkg/cli/i18n"
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
