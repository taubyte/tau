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

// formatSwarmKey formats the given key into a pnet.PSK type.
// It splits the key by "/" and removes any empty elements.
// If the key length is not equal to expectedKeyLength, it returns an error.
// Otherwise, it formats the key into a specific format and returns it as a byte slice.
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

// deleteEmpty removes any empty elements from the given string slice.
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

// setNetworkDomains sets the network domains based on the provided configuration.
// It updates the domainSpecs package variables with the appropriate values.
func setNetworkDomains(conf *config.Source) {
	domainSpecs.WhiteListedDomains = conf.Domains.Whitelist.Postfix
	domainSpecs.TaubyteServiceDomain = regexp.MustCompile(convertToServiceRegex(conf.NetworkFqdn))
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
	domainSpecs.TaubyteHooksDomain = regexp.MustCompile(fmt.Sprintf(`https://patrick.tau.%s`, conf.NetworkFqdn))
}

// convertToServiceRegex converts the given URL to a service regex pattern.
// It splits the URL by ".", and constructs a regex pattern using the network domain.
// The resulting regex pattern is returned as a string.
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
