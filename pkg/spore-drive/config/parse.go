package config

import (
	"github.com/spf13/afero"
	"github.com/taubyte/go-seer"
)

type Parser interface {
	Cloud() CloudParser
	Hosts() HostsParser
	Shapes() ShapesParser
	Auth() AuthParser
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
