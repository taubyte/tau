package storages

import "github.com/taubyte/tau/pkg/schema/basic"

type getter struct {
	*storage
}

func (s *storage) Get() Getter {
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

func (g getter) Match() string {
	return basic.Get[string](g, "match")
}

func (g getter) Regex() bool {
	return basic.Get[bool](g, "useRegex")
}

func (g getter) Type() string {
	var val struct{}
	for _, _type := range []string{"object", "streaming"} {
		if g.Config().Get(_type).Value(&val) == nil {
			return _type
		}
	}

	return ""
}

func (g getter) Public() bool {
	return basic.Get[string](g, "access", "network") == "all"
}

func (g getter) Versioning() bool {
	return basic.Get[bool](g, "object", "versioning")
}

func (g getter) TTL() string {
	return basic.Get[string](g, "streaming", "ttl")
}

func (g getter) Size() string {
	return basic.Get[string](g, g.Type(), "size")
}

func (g getter) SmartOps() (value []string) {
	return basic.Get[[]string](g, "smartops")
}
