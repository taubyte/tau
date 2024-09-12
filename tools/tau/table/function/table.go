package functionTable

import (
	"strconv"
	"strings"

	commonSchema "github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
)

func getTableData(function *structureSpec.Function, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", function.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", function.Name},
		{"Description", function.Description},
		{"Tags", strings.Join(function.Tags, ", ")},
		{"Type", function.Type},
		{"Timeout", commonSchema.TimeToString(function.Timeout)},
		{"Memory", commonSchema.UnitsToString(function.Memory)},
	}...)

	switch function.Type {
	case common.FunctionTypeHttp, common.FunctionTypeHttps:
		toRender = append(toRender, [][]string{
			{"Method", function.Method},
			{"Domains", strings.Join(function.Domains, ", ")},
			{"Paths", strings.Join(function.Paths, ", ")},
		}...)
	case common.FunctionTypeP2P:
		toRender = append(toRender, [][]string{
			{"Protocol", function.Protocol},
			{"Command", function.Command},
			{"Local", strconv.FormatBool(function.Local)},
		}...)
	case common.FunctionTypePubSub:
		toRender = append(toRender, [][]string{
			{"Channel", function.Channel},
			{"Local", strconv.FormatBool(function.Local)},
		}...)
	}

	toRender = append(toRender, [][]string{
		{"Source", function.Source},
		{"Call", function.Call},
	}...)

	return toRender
}
