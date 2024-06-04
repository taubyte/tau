package application

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*application
}

func (a *application) Get() Getter {
	return &getter{a}
}

func (g getter) Id() (value string) {
	return basic.Get[string](g, "id")
}

func (g getter) Name() string {
	return g.application.name
}

func (g getter) Description() (value string) {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() (value []string) {
	return basic.Get[[]string](g, "tags")
}
