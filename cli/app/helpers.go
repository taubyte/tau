package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/pnet"
)

var (
	expectedKeyLength = 6
)

func formatSwarmKey(key string) (pnet.PSK, error) {
	_key := strings.Split(key, "/")
	_key = deleteEmpty(_key)

	if len(_key) != expectedKeyLength {
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])

	return []byte(format), nil
}

func deleteEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	r := make([]string, 0, len(s))
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

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

func convertToProtocolsRegex(url string) string {
	urls := strings.Split(url, ".")
	pRegex := `^([^.]+\.)?tau`
	var network string
	for _, _url := range urls {
		network += fmt.Sprintf(`\.%s`, _url)
	}

	pRegex += network + "$"
	return pRegex
}
