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
	_key := strings.Split(key, "/")							//Split key by '/' delim and convert to array representation
	_key = deleteEmpty(_key)							//remove "" characters from key array (Further filter array)
												// example: foo//bar/ ---> [foo,"",bar]
		
	if len(_key) != expectedKeyLength {						//if len of key array!=6, throw error
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/						
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])				//create new swarmkey using "/" for delim

	return []byte(format), nil							//return key in unsigned 8-bit 
}

// This functions filters s, removing any "" (empty) characters
func deleteEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	r := make([]string, 0, len(s))
	for _, str := range s {							//I think this is similer to python enumerate, where we ignore the index using place holder _
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

// setting domains for use later
func setNetworkDomains(conf *config.Source) {
	domainSpecs.WhiteListedDomains = conf.Domains.Whitelist.Postfix
	domainSpecs.TaubyteServiceDomain = regexp.MustCompile(convertToServiceRegex(conf.NetworkFqdn))
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
	domainSpecs.TaubyteHooksDomain = regexp.MustCompile(fmt.Sprintf(`https://patrick.tau.%s`, conf.NetworkFqdn))
}

//
func convertToServiceRegex(url string) string {
	urls := strings.Split(url, ".")						//split url by '.' delim, and store as array repersentation
	serviceRegex := `^[^.]+\.tau`						//root path which match domain names ending with ".tau".
	var network string	

	///
	// This could be improved using .Join() instead as creating a string like this is expensive. 
	// Strings a not maulable so a new string is created each time.
	///
	for _, _url := range urls {
		network += fmt.Sprintf(`\.%s`, _url)				//create network path
	}

	serviceRegex += network							
	return serviceRegex
}
