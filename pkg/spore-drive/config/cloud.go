package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

type CloudParser interface {
	Domain() DomainParser
	P2P() P2PParser
}

type P2PParser interface {
	Bootstrap() BootstrapParser
	Swarm() SwarmKeyParser
}

type BootstrapParser interface {
	Shape(shape string) ListParser[string]
	Delete(shape string) error

	List() []string
}

type SwarmKeyParser interface {
	Get() string

	Open() (io.ReadCloser, error)
	Create() (io.WriteCloser, error)

	Set(string) error

	Generate() error
}

type DomainParser interface {
	Root() string
	Generated() string
	Validation() ValidationParser

	SetRoot(string) error
	SetGenerated(string) error
}

type ValidationParser interface {
	Keys() (string, string)

	SetPrivateKey(string) error
	SetPublicKey(string) error

	OpenPrivateKey() (io.ReadCloser, error)
	OpenPublicKey() (io.ReadCloser, error)

	CreatePrivateKey() (io.WriteCloser, error)
	CreatePublicKey() (io.WriteCloser, error)

	Generate() error
}

type (
	cloud      leaf
	domain     leaf
	validation leaf
	p2p        leaf
	bootstrap  leaf
	swarm      leaf
)

func (c *cloud) Domain() DomainParser {
	return &domain{root: c.root, Query: c.Fork().Get("domain")}

}

func (d *domain) Root() (r string) {
	d.Fork().Get("root").Value(&r)
	return
}

func (d *domain) Generated() (g string) {
	d.Fork().Get("generated").Value(&g)
	return
}

func (d *domain) Validation() ValidationParser {
	return &validation{root: d.root, Query: d.Fork().Get("validation").Get("key")}
}

func (v *validation) Generate() error {
	priv, pub, err := generateDVKeys(nil, nil)
	if err != nil {
		return fmt.Errorf("generating domain validation keys failed with %w", err)
	}

	v.root.fs.Mkdir("keys", 0700)

	privKey, err := v.CreatePrivateKey()
	if err != nil {
		return err
	}

	pubKey, err := v.CreatePublicKey()
	if err != nil {
		return err
	}

	if _, err = io.Copy(privKey, bytes.NewBuffer(priv)); err != nil {
		return err
	}

	if _, err = io.Copy(pubKey, bytes.NewBuffer(pub)); err != nil {
		return err
	}

	return nil
}

func (v *validation) Keys() (privkey string, pubkey string) {
	v.Fork().Get("private").Value(&privkey)
	v.Fork().Get("public").Value(&pubkey)
	return
}

func (v *validation) SetPrivateKey(path string) error {
	return v.Fork().Get("private").Set(path).Commit()
}

func (v *validation) SetPublicKey(path string) error {
	return v.Fork().Get("public").Set(path).Commit()
}

func (v *validation) OpenPrivateKey() (io.ReadCloser, error) {
	path, _ := v.Keys()
	if path == "" {
		path = "keys/dv_private.key"
		if err := v.SetPrivateKey(path); err != nil {
			return nil, err
		}
	}

	return v.root.fs.Open(path)
}

func (v *validation) OpenPublicKey() (io.ReadCloser, error) {
	_, path := v.Keys()
	if path == "" {
		path = "keys/dv_public.key"
		if err := v.SetPublicKey(path); err != nil {
			return nil, err
		}
	}

	return v.root.fs.Open(path)
}

func (v *validation) CreatePrivateKey() (io.WriteCloser, error) {
	path, _ := v.Keys()
	if path == "" {
		path = "keys/dv_private.key"
		if err := v.SetPrivateKey(path); err != nil {
			return nil, err
		}
	}

	return v.root.fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
}

func (v *validation) CreatePublicKey() (io.WriteCloser, error) {
	_, path := v.Keys()
	if path == "" {
		path = "keys/dv_public.key"
		if err := v.SetPublicKey(path); err != nil {
			return nil, err
		}
	}

	return v.root.fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
}

func (d *domain) SetRoot(r string) error {
	return d.Fork().Get("root").Set(r).Commit()
}

func (d *domain) SetGenerated(g string) error {
	return d.Fork().Get("generated").Set(g).Commit()
}

func (c *cloud) P2P() P2PParser {
	return &p2p{root: c.root, Query: c.Fork().Get("p2p")}
}

func (p *p2p) Bootstrap() BootstrapParser {
	return &bootstrap{root: p.root, Query: p.Fork().Get("bootstrap")}
}

func (b *bootstrap) Shape(shape string) ListParser[string] {
	return &list[string]{root: b.root, Query: b.Fork().Get(shape)}
}

func (b *bootstrap) Delete(shape string) error {
	return b.Fork().Get(shape).Delete().Commit()
}

func (b *bootstrap) List() (l []string) {
	l, _ = b.Fork().List()
	return
}

func (p *p2p) Swarm() SwarmKeyParser {
	return &swarm{root: p.root, Query: p.Fork().Get("swarm").Get("key")}
}

func (s *swarm) Get() (k string) {
	s.Fork().Value(&k)
	return
}

func (s *swarm) Open() (io.ReadCloser, error) {
	path := s.Get()
	if path == "" {
		path = "keys/swarm.key"
		if err := s.Set(path); err != nil {
			return nil, err
		}
	}

	return s.root.fs.Open(path)
}

func (s *swarm) Create() (io.WriteCloser, error) {
	path := s.Get()
	if path == "" {
		path = "keys/swarm.key"
		if err := s.Set(path); err != nil {
			return nil, err
		}
	}

	return s.root.fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
}

func (s *swarm) Set(path string) error {
	return s.Fork().Set(path).Commit()
}

func (s *swarm) Generate() error {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		return fmt.Errorf("rand read failed with: %s", err)
	}

	s.root.fs.Mkdir("keys", 0700)

	swarmKey, err := s.Create()
	if err != nil {
		return err
	}

	if _, err = io.Copy(
		swarmKey,
		bytes.NewBufferString("/key/swarm/psk/1.0.0//base16/"+hex.EncodeToString(key)),
	); err != nil {
		return err
	}

	return nil
}
