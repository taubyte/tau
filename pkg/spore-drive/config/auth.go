package config

import (
	"errors"
	"io"
	"os"
)

type AuthParser interface {
	List() []string
	Get(string) SignerParser
	Add(string) SignerParser
	Delete(string) error
}

type SignerParser interface {
	Username() string
	Password() string
	Key() string
	Open() (io.ReadCloser, error)
	Create() (io.WriteCloser, error)

	SetUsername(string) error
	SetPassword(string) error
	SetKey(string) error
}

type (
	auth   leaf
	signer leaf
)

func (a *auth) List() (l []string) {
	l, _ = a.Fork().List()
	return
}

func (a *auth) Get(name string) SignerParser {
	return &signer{root: a.root, Query: a.Fork().Get(name)}
}

func (a *auth) Add(name string) SignerParser {
	return &signer{root: a.root, Query: a.Fork().Get(name)}
}

func (a *auth) Delete(name string) error {
	return a.Fork().Get(name).Delete().Commit()
}

func (s *signer) Username() (u string) {
	s.Fork().Get("username").Value(&u)
	return
}

func (s *signer) Password() (p string) {
	s.Fork().Get("password").Value(&p)
	return
}

func (s *signer) Key() (k string) {
	s.Fork().Get("key").Value(&k)
	return
}

func (s *signer) Open() (io.ReadCloser, error) {
	path := s.Key()
	if path == "" {
		return nil, errors.New("no key found")
	}

	return s.root.fs.Open(path)
}

func (s *signer) Create() (io.WriteCloser, error) {
	path := s.Key()
	if path == "" {
		return nil, errors.New("no key found")
	}

	return s.root.fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
}

func (s *signer) SetUsername(name string) error {
	return s.Fork().Get("username").Set(name).Commit()
}

func (s *signer) SetPassword(password string) error {
	s.Fork().Get("key").Delete().Commit()
	return s.Fork().Get("password").Set(password).Commit()
}

func (s *signer) SetKey(path string) error {
	s.Fork().Get("password").Delete().Commit()
	return s.Fork().Get("key").Set(path).Commit()
}
