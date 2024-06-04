package functions

import "github.com/taubyte/tau/pkg/schema/basic"

type getter struct {
	*function
}

func (f *function) Get() Getter {
	return getter{f}
}

func (g getter) Name() string {
	return g.function.name
}

func (g getter) Application() string {
	return g.function.application
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

func (g getter) Type() string {
	return basic.Get[string](g, "trigger", "type")
}

func (g getter) Method() string {
	return basic.Get[string](g, "trigger", "method")
}

func (g getter) Paths() []string {
	return basic.Get[[]string](g, "trigger", "paths")
}

func (g getter) Local() bool {
	return basic.Get[bool](g, "trigger", "local")
}

func (g getter) Command() string {
	return basic.Get[string](g, "trigger", "command")
}

func (g getter) Channel() string {
	return basic.Get[string](g, "trigger", "channel")
}

func (g getter) Protocol() string {
	return basic.Get[string](g, "trigger", "service")
}

func (g getter) Source() string {
	return basic.Get[string](g, "source")
}

func (g getter) Domains() []string {
	return basic.Get[[]string](g, "domains")
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

func (g getter) SmartOps() (smartops []string) {
	return basic.Get[[]string](g, "smartops")
}
