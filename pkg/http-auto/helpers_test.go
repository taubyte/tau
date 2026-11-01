package auto

import (
	"strings"
	"testing"

	tns "github.com/taubyte/tau/core/services/tns"
	"gotest.tools/v3/assert"
)

// flatTNS mimics the TNS engine's Get: every key under the fetched path is
// returned, a single key collapses to its bare value, several keys become a
// nested map.
type flatTNS struct {
	tns.Client
	keys map[string]any
}

type flatObject struct {
	MockTNSObject
	value any
}

func (o *flatObject) Interface() any { return o.value }

func (f *flatTNS) Fetch(path tns.Path) (tns.Object, error) {
	prefix := path.String()
	matches := map[string]any{}
	for k, v := range f.keys {
		if k == prefix || strings.HasPrefix(k, prefix+"/") {
			matches[strings.TrimPrefix(strings.TrimPrefix(k, prefix), "/")] = v
		}
	}

	var value any
	switch len(matches) {
	case 0:
	case 1:
		for _, v := range matches {
			value = v
		}
	default:
		root := map[string]any{}
		for rel, v := range matches {
			cur := root
			parts := strings.Split(rel, "/")
			for _, part := range parts[:len(parts)-1] {
				next, ok := cur[part].(map[string]any)
				if !ok {
					next = map[string]any{}
					cur[part] = next
				}
				cur = next
			}
			cur[parts[len(parts)-1]] = v
		}
		value = root
	}

	return &flatObject{MockTNSObject: MockTNSObject{path: path}, value: value}, nil
}

const (
	apexProject = "QmTgYkGBqTjodax63a8fGZR9eagMKkXjv7L32fHLm9dqCw"
	subProjectA = "QmXrAk1VW8FvgNbjXYydsJuwfKYqYqanztM61QGBR8v4Wq"
)

func TestGetProjectIdApexWithSubdomainFunctions(t *testing.T) {
	keys := map[string]any{
		"http/websites/dz/ac/links":               []any{"branches/main/projects/" + apexProject + "/websites/QmYdX7uAaUq3AF9Ap5VJ69dx5J7u3HQekVoJbXCXQt2abo"},
		"http/websites/dz/ac/at/hlm9dqcw0/links":  []any{"branches/main/projects/" + apexProject + "/websites/QmYdX7uAaUq3AF9Ap5VJ69dx5J7u3HQekVoJbXCXQt2abo"},
		"http/functions/dz/ac/at/hlm9dqcw0/links": []any{"branches/main/projects/" + apexProject + "/functions/QmaaU9tsqUGHMD1VsrmByjzHVwrX9Urm6aZNDK3n8jFcVh"},
		"http/functions/dz/ac/at/gbr8v4wq0/links": []any{"branches/main/projects/" + subProjectA + "/applications/QmZ2r6zb3XiWvLzhww5mdKs3KYii2sr6KG9ZbvpfFBNDPE/functions/QmXyTgtc1xfNsm3GUW2utgDzrYLW4EZXe4xuyFXVf1Hpom"},
	}
	s := &Service{tnsClient: &flatTNS{keys: keys}}

	pid, err := s.getProjectId("ac.dz")
	assert.NilError(t, err)
	assert.Equal(t, pid, strings.ToLower(apexProject))

	pid, err = s.getProjectId("gbr8v4wq0.at.ac.dz")
	assert.NilError(t, err)
	assert.Equal(t, pid, strings.ToLower(subProjectA))
}

func TestGetProjectIdUnknownHost(t *testing.T) {
	s := &Service{tnsClient: &flatTNS{keys: map[string]any{
		"http/functions/dz/ac/at/hlm9dqcw0/links": []any{"branches/main/projects/" + apexProject + "/functions/QmaaU9tsqUGHMD1VsrmByjzHVwrX9Urm6aZNDK3n8jFcVh"},
	}}}

	_, err := s.getProjectId("ac.dz")
	assert.ErrorContains(t, err, "nil interface")
}
