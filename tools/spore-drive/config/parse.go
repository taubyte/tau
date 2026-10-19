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
	Sync() error
}

type parser struct {
	*seer.Seer
	fs afero.Fs
}

type leaf struct {
	//mode leafMode
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
