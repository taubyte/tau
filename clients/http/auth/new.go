package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	git "github.com/taubyte/tau/clients/http/auth/git"
)

// New returns a new Client based on the options provided and an error
func New(ctx context.Context, options ...Option) (*Client, error) {
	c := &Client{
		timeout:  DefaultTimeout,
		ctx:      ctx,
		unsecure: false,
	}

	for _, opt := range options {
		err := opt(c)
		if err != nil {
			return nil, fmt.Errorf("when Creating Auth HTTP Client, parsing options failed with: %s", err.Error())
		}
	}

	c.client = &http.Client{
		Timeout: c.timeout,
	}

	if !c.unsecure {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	} else {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	c.auth_header = fmt.Sprintf("%s %s", c.provider, c.token)
	c.gitClient = git.New(c.ctx, c.provider, c.token)

	return c, nil
}
