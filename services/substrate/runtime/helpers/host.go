package helpers

import (
	"strings"
)

func ExtractHost(hostAndPort string) string {
	return strings.ToLower(strings.Split(hostAndPort, ":")[0])
}
