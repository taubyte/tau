package config

import (
	"io"
	"time"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/p2p/sectls"
)

type CloudParser interface {
	Domain() DomainParser
	P2P() P2PParser
	Auth() (Auth, error)
	CM() (CM, error)
	AccountKey(ACME) ([]byte, error)
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

	SetRoot(string) error
	SetGenerated(string) error

	// Hosts binds custom domains to services (domains.hosts), e.g. "console.<fqdn>" ->
	// "gateway".
	Hosts() map[string]string
	SetHost(domain, service string) error
	DeleteHost(domain string) error
}

type (
	cloud     leaf
	domain    leaf
	p2p       leaf
	bootstrap leaf
	authority leaf
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

// Hosts stores the whole map in one node, since yaseer would read the dots in a
// domain key as path separators.
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
