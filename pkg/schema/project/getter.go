package project

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*project
}

func (p *project) Get() Getter {
	return &getter{p}
}

func (g getter) Id() string {
	return basic.Get[string](g, "id")
}

func (g getter) Name() string {
	return basic.Get[string](g, "name")
}

func (g getter) Description() string {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() []string {
	return basic.Get[[]string](g, "tags")
}

func (g getter) Email() string {
	return basic.Get[string](g, "notification", "email")
}

func (g getter) Applications() []string {
	apps, err := g.seer.Get("applications").List()
	if err != nil {
		return nil
	}

	return apps
}
