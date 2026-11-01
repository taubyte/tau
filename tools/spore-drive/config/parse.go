package config

import (
	"github.com/spf13/afero"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Parser interface {
	Cloud() CloudParser
	Hosts() HostsParser
	Shapes() ShapesParser
	Auth() AuthParser
	Added() map[string]any
	Tenancy() []string
	TenancyApp() bool
	Sync() error
}

type parser struct {
	*seer.Seer
	fs afero.Fs
}

type leaf struct {
	root *parser
	*seer.Query
}

func New(fs afero.Fs, path string) (Parser, error) {
	s, err := seer.New(seer.VirtualFS(fs, path))
	if err != nil {
		return nil, err
	}

	return &parser{Seer: s, fs: afero.NewBasePathFs(fs, path)}, err
}

func (p *parser) Cloud() CloudParser {
	return &cloud{root: p, Query: p.Get("cloud").Document()}
}

func (p *parser) Hosts() HostsParser {
	return &hosts{root: p, Query: p.Get("hosts").Document()}
}

func (p *parser) Shapes() ShapesParser {
	return &shapes{root: p, Query: p.Get("shapes").Document()}
}

func (p *parser) Auth() AuthParser {
	return &auth{root: p, Query: p.Get("auth").Document()}
}

// Added is the configuration of each service a node is built with beyond the
// ones tau ships, keyed by the name it is added by: added.yaml, copied into
// every node's `node.added` unread. Which services those are is the binary's
// business; a node built without one refuses its entry by name.
func (p *parser) Added() map[string]any {
	var m map[string]any
	p.Get("added").Document().Value(&m)
	return m
}

// Tenancy is the forge namespaces the cloud serves, from tenancy.yaml, each as
// host/namespace. The file names them under `namespaces`, and the one-owner
// form — `provider` (github, gitlab, or a forge host) and `owner` — is one
// more. Nothing here canonicalises: the node does, and refuses a malformed
// entry by name.
func (p *parser) Tenancy() []string {
	doc := p.Get("tenancy").Document()
	var out []string
	doc.Get("namespaces").Value(&out)
	var provider, owner string
	doc.Get("provider").Value(&provider)
	doc.Get("owner").Value(&owner)
	if owner != "" {
		host, known := publicForges[provider]
		if !known {
			host = provider
		}
		out = append(out, host+"/"+owner)
	}
	return out
}

// TenancyApp reports whether tenancy.yaml carries an app credential, which no
// v2 node reads.
func (p *parser) TenancyApp() bool {
	var app map[string]any
	p.Get("tenancy").Document().Get("app").Value(&app)
	return len(app) > 0
}

var publicForges = map[string]string{"github": "github.com", "gitlab": "gitlab.com"}
