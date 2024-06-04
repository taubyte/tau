package services

import "github.com/taubyte/tau/pkg/schema/basic"

type getter struct {
	*service
}

func (s *service) Get() Getter {
	return getter{s}
}

func (g getter) Name() string {
	return g.name
}

func (g getter) Application() string {
	return g.application
}

func (g getter) Id() string {
	return basic.Get[string](g, "id")
}

func (g getter) Description() string {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() []string {
	return basic.Get[[]string](g, "tags")
}

func (g getter) Protocol() string {
	return basic.Get[string](g, "protocol")
}

func (g getter) SmartOps() []string {
	return basic.Get[[]string](g, "smartops")
}
