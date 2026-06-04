package website

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type getter struct {
	*website
}

func (g getter) Bindings() []structureSpec.Binding {
	return basic.Get[[]structureSpec.Binding](g, "bindings")
}

func (w *website) Get() Getter {
	return getter{w}
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

func (g getter) Domains() []string {
	return basic.Get[[]string](g, "domains")
}

func (g getter) Paths() []string {
	return basic.Get[[]string](g, "source", "paths")
}
func (g getter) Branch() string {
	return basic.Get[string](g, "source", "branch")
}

func (g getter) Git() (provider, id, fullname string) {
	for _, provider = range []string{"github"} {
		data := make(map[string]string)
		if g.Config().Get("source").Get(provider).Value(&data) == nil {
			id = data["id"]
			fullname = data["fullname"]
			return
		}
	}

	return "", "", ""
}

func (g getter) SmartOps() (value []string) {
	return basic.Get[[]string](g, "smartops")
}

func (g getter) Render() string {
	return basic.Get[string](g, "render")
}

func (g getter) Framework() string {
	return basic.Get[string](g, "framework")
}

func (g getter) Entry() string {
	return basic.Get[string](g, "entry")
}
