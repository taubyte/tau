package smartops

import "github.com/taubyte/tau/pkg/schema/basic"

type getter struct {
	*smartOps
}

func (s *smartOps) Get() Getter {
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

func (g getter) Source() string {
	return basic.Get[string](g, "source")
}

func (g getter) Timeout() string {
	return basic.Get[string](g, "execution", "timeout")
}

func (g getter) Memory() string {
	return basic.Get[string](g, "execution", "memory")
}

func (g getter) Call() string {
	return basic.Get[string](g, "execution", "call")
}

func (g getter) SmartOps() (value []string) {
	return basic.Get[[]string](g, "smartops")
}
