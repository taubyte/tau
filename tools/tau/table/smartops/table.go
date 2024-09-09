package smartopsTable

import (
	"strings"

	commonSchema "github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func getTableData(smartops *structureSpec.SmartOp, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", smartops.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", smartops.Name},
		{"Description", smartops.Description},
		{"Tags", strings.Join(smartops.Tags, ", ")},
		{"Timeout", commonSchema.TimeToString(smartops.Timeout)},
		{"Memory", commonSchema.UnitsToString(smartops.Memory)},
		{"Source", smartops.Source},
		{"Call", smartops.Call},
	}...)

	return toRender
}
