package app

import (
	"strings"
)

func convertToPostfixRegex(url string) string {
	return `^[^.]+\.` + strings.Join(strings.Split(url, "."), `\.`) + "$"
}

func convertToServicesRegex(url string) string {
	return `^[^.]+\.tau\.` + strings.Join(strings.Split(url, "."), `\.`) + `$`
}
