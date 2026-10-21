package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/p2p/sectls"
)

type CloudParser interface {
	Domain() DomainParser
	P2P() P2PParser
}

type P2PParser interface {
	Bootstrap() BootstrapParser
	CA() CAParser
}

type BootstrapParser interface {
	Shape(shape string) ListParser[string]
	Delete(shape string) error

	List() []string
}

// CAParser is the cloud's certificate authority: a root certificate and its
// key, both PEM, which spore-drive issues every node's certificate from at
// deploy. Generate mints one; a user who owns a CA puts its files at the paths
// instead.
type CAParser interface {
	Paths() (certificate string, key string)

	SetCertificate(string) error
	SetKey(string) error

	OpenCertificate() (io.ReadCloser, error)
	OpenKey() (io.ReadCloser, error)

	Load() (*sectls.CA, error)
	Generate() error
}

type DomainParser interface {
	Root() string
	Generated() string
	Validation() ValidationParser

	SetRoot(string) error
	SetGenerated(string) error

	// Hosts binds custom domains to services (domains.hosts): e.g.
	// "console.<fqdn>" -> "gateway". Generic (any domain -> any service).
	Hosts() map[string]string
	SetHost(domain, service string) error
	DeleteHost(domain string) error
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
	authority  leaf
)

func (c *cloud) Domain() DomainParser {
	return &domain{root: c.root, Query: c.Query.Get("domain")}

}

func (d *domain) Root() (r string) {
	d.Query.Get("root").Value(&r)
	return
}

func (d *domain) Generated() (g string) {
	d.Query.Get("generated").Value(&g)
	return
}

func (d *domain) Validation() ValidationParser {
	return &validation{root: d.root, Query: d.Query.Get("validation").Get("key")}
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
	v.Query.Get("private").Value(&privkey)
	v.Query.Get("public").Value(&pubkey)
	return
}

func (v *validation) SetPrivateKey(path string) error {
	return v.Query.Get("private").Set(path).Commit()
}

func (v *validation) SetPublicKey(path string) error {
	return v.Query.Get("public").Set(path).Commit()
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

// Hosts stores the whole domain->service map in one node (domains contain dots,
// which yaseer would treat as path separators if used as keys).
func (d *domain) Hosts() map[string]string {
	m := make(map[string]string)
	d.Query.Get("hosts").Value(&m)
	return m
}

func (d *domain) SetHost(host, service string) error {
	m := d.Hosts()
	m[host] = service
	return d.Query.Get("hosts").Set(m).Commit()
}

func (d *domain) DeleteHost(host string) error {
	m := d.Hosts()
	delete(m, host)
	return d.Query.Get("hosts").Set(m).Commit()
}

func (d *domain) SetRoot(r string) error {
	return d.Query.Get("root").Set(r).Commit()
}

func (d *domain) SetGenerated(g string) error {
	return d.Query.Get("generated").Set(g).Commit()
}

func (c *cloud) P2P() P2PParser {
	return &p2p{root: c.root, Query: c.Query.Get("p2p")}
}

func (p *p2p) Bootstrap() BootstrapParser {
	return &bootstrap{root: p.root, Query: p.Query.Get("bootstrap")}
}

func (b *bootstrap) Shape(shape string) ListParser[string] {
	return &list[string]{root: b.root, Query: b.Query.Get(shape)}
}

func (b *bootstrap) Delete(shape string) error {
	return b.Query.Get(shape).Delete().Commit()
}

func (b *bootstrap) List() (l []string) {
	l, _ = b.Query.List()
	return
}

func (p *p2p) CA() CAParser {
	return &authority{root: p.root, Query: p.Query.Get("ca")}
}

const (
	caCertificatePath = "keys/ca.crt"
	caKeyPath         = "keys/ca.key"

	// caValidity is long because rotating a CA is redeploying every node.
	caValidity = 10 * 365 * 24 * time.Hour
)

func (a *authority) Paths() (certificate string, key string) {
	a.Query.Get("certificate").Value(&certificate)
	a.Query.Get("key").Value(&key)
	return
}

func (a *authority) SetCertificate(path string) error {
	return a.Query.Get("certificate").Set(path).Commit()
}

func (a *authority) SetKey(path string) error {
	return a.Query.Get("key").Set(path).Commit()
}

func (a *authority) paths() (string, string, error) {
	certificate, key := a.Paths()
	if certificate == "" {
		certificate = caCertificatePath
		if err := a.SetCertificate(certificate); err != nil {
			return "", "", err
		}
	}
	if key == "" {
		key = caKeyPath
		if err := a.SetKey(key); err != nil {
			return "", "", err
		}
	}
	return certificate, key, nil
}

func (a *authority) OpenCertificate() (io.ReadCloser, error) {
	certificate, _, err := a.paths()
	if err != nil {
		return nil, err
	}
	return a.root.fs.Open(certificate)
}

func (a *authority) OpenKey() (io.ReadCloser, error) {
	_, key, err := a.paths()
	if err != nil {
		return nil, err
	}
	return a.root.fs.Open(key)
}

func (a *authority) Load() (*sectls.CA, error) {
	certificate, key, err := a.paths()
	if err != nil {
		return nil, err
	}
	certPEM, err := afero.ReadFile(a.root.fs, certificate)
	if err != nil {
		return nil, err
	}
	keyPEM, err := afero.ReadFile(a.root.fs, key)
	if err != nil {
		return nil, err
	}
	return sectls.LoadCA(certPEM, keyPEM)
}

func (a *authority) Generate() error {
	ca, err := sectls.NewCA(a.root.Cloud().Domain().Root(), caValidity)
	if err != nil {
		return err
	}
	keyPEM, err := ca.KeyPEM()
	if err != nil {
		return err
	}
	certificate, key, err := a.paths()
	if err != nil {
		return err
	}
	a.root.fs.Mkdir("keys", 0700)
	if err := afero.WriteFile(a.root.fs, key, keyPEM, 0600); err != nil {
		return err
	}
	return afero.WriteFile(a.root.fs, certificate, ca.CertificatePEM(), 0600)
}
