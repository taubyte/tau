package url

import (
	"fmt"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/pkg/vm/backend/errors"
	resolv "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
)

func buildUri(multiAddr ma.Multiaddr) (uri string, err error) {
	var scheme, host, path string

	protocols := multiAddr.Protocols()
	if len(protocols) < 2 {
		return "", errors.MultiAddrCompliant(multiAddr, "url")
	}

	host, err = getHost(protocols[0], multiAddr)
	if err != nil {
		return
	}

	scheme, err = getScheme(protocols[1])
	if err != nil {
		return
	}

	if len(protocols) > 2 {
		path, err = getPath(protocols[2], multiAddr)
		if err != nil {
			return
		}
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, path), nil
}

func getHost(protocol ma.Protocol, multiAddr ma.Multiaddr) (host string, err error) {
	switch protocol.Code {
	case ma.P_DNS, ma.P_DNS4, ma.P_DNS6, ma.P_IP4, ma.P_IP6:
		host, err = multiAddr.ValueForProtocol(protocol.Code)
		if err != nil {
			err = errors.ParseProtocol(protocol.Name, err)
			return
		}
		if len(host) < 1 {
			err = fmt.Errorf("no host found")
		}
	default:
		err = errors.MultiAddrCompliant(multiAddr, "url")
	}

	return
}

func getScheme(protocol ma.Protocol) (scheme string, err error) {
	switch protocol.Code {
	case ma.P_HTTP, ma.P_HTTPS:
		scheme = protocol.Name
	default:
		err = fmt.Errorf("uri scheme not defined")
	}

	return
}

func getPath(protocol ma.Protocol, multiAddr ma.Multiaddr) (string, error) {
	if protocol.Name != resolv.PATH_PROTOCOL_NAME {
		return "", fmt.Errorf("expected path protocol got `%s`", protocol.Name)
	}

	path, err := multiAddr.ValueForProtocol(resolv.P_PATH)
	if err != nil {
		return "", errors.ParseProtocol(resolv.PATH_PROTOCOL_NAME, err)
	}

	return path, nil
}
