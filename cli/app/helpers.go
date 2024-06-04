package app

import (
	"fmt"
	"strings"
)

func convertToPostfixRegex(url string) string {
	urls := strings.Split(url, ".")
	pRegex := `^([^.]+\.)?`
	var network string
	for _, _url := range urls {
		network += fmt.Sprintf(`\.%s`, _url)
	}

	pRegex += network[2:] + "$" // skip the first "\."
	return pRegex
}

func convertToServicesRegex(url string) string {
	urls := strings.Split(url, ".")
	pRegex := `^([^.]+\.)?tau`
	var network string
	for _, _url := range urls {
		network += fmt.Sprintf(`\.%s`, _url)
	}

	pRegex += network + "$"
	return pRegex
}
