package databases

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*database
}

func (d *database) Get() Getter {
	return getter{d}
}

func (g getter) Name() string {
	return g.database.name
}

func (g getter) Application() string {
	return g.database.application
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

func (g getter) Local() bool {
	network := basic.Get[string](g, "access", "network")
	return (network == "host")
}

// Secret returns true if a value for encryption is set in the yaml
func (d getter) Secret() bool {
	var val struct{}
	return (d.Config().Get("encryption").Value(&val) == nil)
}

func (g getter) Encryption() (key string, keyType string) {
	enc := g.Config().Get("encryption")

	enc.Fork().Get("key").Value(&key)
	enc.Fork().Get("type").Value(&keyType)

	return
}

func (g getter) Min() int {
	return basic.Get[int](g, "replicas", "min")
}

func (g getter) Max() int {
	return basic.Get[int](g, "replicas", "max")
}

func (g getter) Size() string {
	return basic.Get[string](g, "storage", "size")
}

func (g getter) SmartOps() (value []string) {
	return basic.Get[[]string](g, "smartops")
}
