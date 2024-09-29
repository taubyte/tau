package auth

import (
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

type Option func(*Auth) error

func Password(password string) Option {
	return func(a *Auth) error {
		a.Auth = append(a.Auth, ssh.Password(password))
		return nil
	}
}

func Key(r io.Reader) Option {
	return func(a *Auth) error {
		privateKey, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(privateKey)
		if err != nil {
			fmt.Println(string(privateKey))
			return fmt.Errorf("parsing private key: %w", err)
		}
		a.Auth = append(a.Auth, ssh.PublicKeys(signer))
		return nil
	}
}

func KeyWithPassphrase(r io.Reader, passphrase string) Option {
	return func(a *Auth) error {
		privateKey, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKeyWithPassphrase(privateKey, []byte(passphrase))
		if err != nil {
			return fmt.Errorf("parsing private key with passphrase: %w", err)
		}
		a.Auth = append(a.Auth, ssh.PublicKeys(signer))
		return nil
	}
}
