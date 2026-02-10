package databaseTable

import (
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func getCloudDisplay(local bool) []string {
	if local {
		return []string{"\tCloud", "host"}
	}

	return []string{"\tCloud", "all"}
}

func getTableData(database *structureSpec.Database, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", database.Id},
		}
	}

	secret := len(database.Key) > 0

	toRender = append(toRender, [][]string{
		{"Name", database.Name},
		{"Description", database.Description},
		{"Tags", strings.Join(database.Tags, ", ")},
		{"Encryption", strconv.FormatBool(secret)},
		{"Access", ""},
		getCloudDisplay(database.Local),
		{"Replicas", ""},
		{"\tMin", strconv.Itoa(int(database.Min))},
		{"\tMax", strconv.Itoa(int(database.Max))},
		{"Storage", ""},
		{"\tSize", common.UnitsToString(database.Size)},
	}...)

	return toRender
}
