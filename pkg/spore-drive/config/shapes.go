package config

type ShapesParser interface {
	List() []string
	Shape(string) ShapeParser
	Delete(string) error
}

type ShapeParser interface {
	Services() ListParser[string]
	Ports() PortsParser
	Plugins() ListParser[string]
}

type PortsParser interface {
	List() []string
	Get(string) uint16
	Set(string, uint16) error
	Delete(string) error
}

type (
	shapes leaf
	shape  leaf
	ports  leaf
)

func (s *shapes) List() (l []string) {
	l, _ = s.Fork().List()
	return
}

func (s *shapes) Shape(name string) ShapeParser {
	return &shape{root: s.root, Query: s.Fork().Get(name)}
}

func (s *shapes) Delete(name string) error {
	return s.Fork().Get(name).Delete().Commit()
}

func (s *shape) Services() ListParser[string] {
	return &list[string]{root: s.root, Query: s.Fork().Get("services")}
}

func (s *shape) Ports() PortsParser {
	return &ports{root: s.root, Query: s.Fork().Get("ports")}
}

func (s *shape) Plugins() ListParser[string] {
	return &list[string]{root: s.root, Query: s.Fork().Get("plugins")}
}

func (p *ports) List() (l []string) {
	l, _ = p.Fork().List()
	return
}

func (p *ports) Get(name string) uint16 {
	var prt int
	p.Fork().Get(name).Value(&prt)
	return uint16(prt)
}

func (p *ports) Set(name string, prt uint16) error {
	return p.Fork().Get(name).Set(int(prt)).Commit()
}

func (p *ports) Delete(name string) error {
	return p.Fork().Get(name).Delete().Commit()
}
