package validate

import (
	schemaCommon "github.com/taubyte/go-project-schema/common"
	"github.com/taubyte/tau-cli/i18n"
)

func Time(s string) error {
	val, err := schemaCommon.StringToTime(s)
	if err != nil {
		return err
	}

	if val == 0 {
		return i18n.Time0Invalid
	}

	return nil
}
