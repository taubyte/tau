package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/pnet"
)

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

var (
	expectedKeyLength = 6
)

var (
	frames = []string{"∙∙∙", "●∙∙", "∙●∙", "∙∙●", "∙∙∙"}
)

func formatSwarmKey(key []byte) (pnet.PSK, error) {
	_key := strings.Split(string(key), "/")
	_key = deleteEmpty(_key)

	if len(_key) != expectedKeyLength {
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])

	return []byte(format), nil
}
