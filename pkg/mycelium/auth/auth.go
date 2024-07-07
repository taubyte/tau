package auth

import (
	"fmt"

	"golang.org/x/crypto/ssh"
)

type Auth struct {
	Username string
	Auth     []ssh.AuthMethod
}

func New(username string, options ...Option) (*Auth, error) {
	a := &Auth{Username: username}
	for _, opt := range options {
		if err := opt(a); err != nil {
			return nil, fmt.Errorf("applying option: %w", err)
		}
	}
	return a, nil
}
