package app

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/libp2p/go-libp2p/core/pnet"
	domainSpecs "github.com/taubyte/go-specs/domain"
	"github.com/taubyte/tau/config"
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

func setNetworkDomains(conf *config.Source) {
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
}

func convertToServiceRegex(url string) string {
	urls := strings.Split(url, ".")
	serviceRegex := `^[^.]+\.tau`
	var network string
	for _, _url := range urls {
		network += fmt.Sprintf(`\.%s`, _url)
	}

	serviceRegex += network
	return serviceRegex
}
