package host

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/taubyte/tau/pkg/mycelium/auth"
	"golang.org/x/crypto/ssh"
)

type Attribute func(Host) error

func Timeout(ttl time.Duration) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.timeout = ttl
		return nil
	}
}

func PublicKey(key io.Reader) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		keyBytes, err := io.ReadAll(key)
		if err != nil {
			return fmt.Errorf("reading public key: %w", err)
		}

		parsedKey, _, _, _, err := ssh.ParseAuthorizedKey(keyBytes)
		if err != nil {
			return fmt.Errorf("parsing public key: %w", err)
		}

		h.key = parsedKey

		return nil
	}
}

func Address(addr string) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		host, portStr, err := net.SplitHostPort(addr)
		if err == nil && portStr != "" {
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return fmt.Errorf("parsing port: %w", err)
			}
			h.port = port
		} else {
			host = addr
		}

		h.addr = host

		return nil
	}
}

func Port(port uint16) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.port = uint64(port)
		return nil
	}
}

func Auth(a *auth.Auth) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.auth = append(h.auth, a)
		return nil
	}
}

func Auths(a ...*auth.Auth) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.auth = a
		return nil
	}
}

func Name(name string) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.name = name
		return nil
	}
}

func Tag(tag string) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.tags = append(h.tags, tag)
		return nil
	}
}

func Tags(tags ...string) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		h.tags = tags
		return nil
	}
}

func Password(username, password string) Attribute {
	return func(hi Host) error {
		h := hi.(*host)
		if a, err := auth.New(username, auth.Password(password)); err != nil {
			return fmt.Errorf("creating auth with password: %w", err)
		} else {
			h.auth = append(h.auth, a)
		}

		return nil
	}
}

func Key(username string, passphrase string, key io.Reader) Attribute {
	return func(hi Host) error {
		h := hi.(*host)

		var a *auth.Auth
		var err error

		if passphrase != "" {
			a, err = auth.New(username, auth.KeyWithPassphrase(key, passphrase))
		} else {
			a, err = auth.New(username, auth.Key(key))
		}

		if err != nil {
			return fmt.Errorf("creating auth with key: %w", err)
		}

		h.auth = append(h.auth, a)

		return nil
	}
}
