package prompts

import (
	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetOrRequireATimeout(ctx *cli.Context, prev ...uint64) (uint64, error) {
	var prevString string
	if len(prev) > 0 {
		prevString = schemaCommon.TimeToString(prev[0])
	}

	stringTimeout := GetOrRequireAString(ctx, flags.Timeout.Name, TimeoutPrompt, validate.Time, prevString)
	return schemaCommon.StringToTime(stringTimeout)
}
