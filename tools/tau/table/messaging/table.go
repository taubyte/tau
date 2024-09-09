package messagingTable

import (
	"strconv"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func getTableData(messaging *structureSpec.Messaging, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", messaging.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", messaging.Name},
		{"Description", messaging.Description},
		{"Tags", strings.Join(messaging.Tags, ", ")},
		{"Local", strconv.FormatBool(messaging.Local)},
		{"Channel", ""},
		{"\tMatch", messaging.Match},
		{"\tUse Regex", strconv.FormatBool(messaging.Regex)},
		{"Bridges", ""},
		{"\tMQTT", strconv.FormatBool(messaging.MQTT)},
		{"\tWebSocket", strconv.FormatBool(messaging.WebSocket)},
	}...)

	return toRender
}
