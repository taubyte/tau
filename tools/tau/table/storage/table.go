package storageTable

import (
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func getNetworkDisplay(public bool) []string {
	if public {
		return []string{"\tNetwork", "all"}
	}

	return []string{"\tNetwork", "host"}
}

func getTableData(storage *structureSpec.Storage, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", storage.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", storage.Name},
		{"Description", storage.Description},
		{"Tags", strings.Join(storage.Tags, ", ")},
		{"Access", ""},
		getNetworkDisplay(storage.Public),
	}...)

	switch storage.Type {
	case "Object":
		toRender = append(toRender, [][]string{
			{storage.Type, ""},
			{"\tVersioning", strconv.FormatBool(storage.Versioning)},
			{"\tSize", common.UnitsToString(storage.Size)},
		}...)
	case "Streaming":
		_time := common.TimeToString(storage.Ttl)
		parsedTime, err := time.ParseDuration(_time)
		if err == nil {
			_time = parsedTime.Abs().String()
		}

		toRender = append(toRender, [][]string{
			{storage.Type, ""},
			{"\tTTL", _time},
			{"\tSize", common.UnitsToString(storage.Size)},
		}...)
	}

	return toRender
}
