package course

import (
	"errors"
	"fmt"
	"slices"

	"github.com/moby/moby/pkg/namesgenerator"
	"github.com/taubyte/tau/pkg/mycelium"
	"github.com/taubyte/tau/pkg/mycelium/host"
)

var DefaultConcurrency = 4

type Host interface {
	Tags() []string
	String() string
}

type Hypha struct {
	Name        string
	Subnet      *mycelium.Network
	Shapes      []string
	Concurrency int
}

type Hyphae []*Hypha

func (hs Hyphae) Size() (l int) {
	for _, h := range hs {
		l += h.Subnet.Size()
	}
	return
}

type Course interface {
	Hyphae() Hyphae
}

type course struct {
	network     *mycelium.Network
	shapes      []string
	concurrency int
}

type Option func(Course) error

func Shape(shape string) Option {
	return func(c Course) error {
		cc, ok := c.(*course)
		if !ok {
			return errors.New("not supported course type")
		}
		cc.shapes = append(cc.shapes, shape)
		return nil
	}
}

func Shapes(shapes ...string) Option {
	return func(c Course) error {
		cc, ok := c.(*course)
		if !ok {
			return errors.New("not supported course type")
		}
		cc.shapes = shapes
		return nil
	}
}

func Concurrency(count int) Option {
	return func(c Course) error {
		cc, ok := c.(*course)
		if !ok {
			return errors.New("not supported course type")
		}
		cc.concurrency = count
		return nil
	}
}

func (c *course) Hyphae() (hyphae Hyphae) {
	// current strategy is simple: each jump is a shape
	for _, shape := range c.shapes {
		stag := fmt.Sprintf("shape[%s]", shape)
		hyphae = append(hyphae, &Hypha{
			Name: namesgenerator.GetRandomName(0),
			Subnet: c.network.Sub(func(h host.Host) bool {
				return slices.Contains(h.Tags(), stag)
			}),
			Shapes:      []string{shape},
			Concurrency: c.concurrency,
		})
	}
	return
}

func New(network *mycelium.Network, options ...Option) (Course, error) {
	c := &course{
		concurrency: DefaultConcurrency,
		network:     network,
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}
