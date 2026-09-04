package importcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
	"github.com/taubyte/tau/pkg/importer/coolify"
	"github.com/taubyte/tau/pkg/importer/dokploy"
	"github.com/taubyte/tau/pkg/importer/fly"
	"github.com/taubyte/tau/pkg/importer/lambda"
	"github.com/taubyte/tau/pkg/importer/vercel"
)

// detect.go picks an importer.
//
// THE FILENAME IS A HINT AND THE CONTENT IS THE ANSWER. `docker-compose.yml`
// and `compose.yaml` are the same file under four names, a Coolify or Dokploy
// app IS a compose file, and a SAM template and a Serverless Framework one are
// both `serverless.yml` to a glance. So the name narrows and the content
// decides, and when neither is conclusive the command says which formats it
// tried rather than guessing.

// sources is every format this command reads, in the order it tries them.
var sources = map[string]func([]byte) (importer.Result, error){
	compose.Source: compose.Import,
	vercel.Source:  vercel.Import,
	fly.Source:     fly.Import,
	coolify.Source: coolify.Import,
	dokploy.Source: dokploy.Import,
	lambda.Source:  lambda.Import,
}

// ErrUnrecognised is a file none of the importers claims.
var ErrUnrecognised = errors.New("import: this file is not one of the formats tau reads")

// importOf reads data with the named importer, or works out which one.
func importOf(from, path string, data []byte) (importer.Result, error) {
	if from != "" {
		read, ok := sources[from]
		if !ok {
			return importer.Result{}, fmt.Errorf("import: %q is not a source tau reads; try one of %s",
				from, strings.Join(names(), ", "))
		}
		return read(data)
	}

	guess := guessFrom(path, data)
	if guess == "" {
		return importer.Result{}, fmt.Errorf("%w: tried %s — name it with --from",
			ErrUnrecognised, strings.Join(names(), ", "))
	}
	return sources[guess](data)
}

// guessFrom names the format, or "".
//
// COOLIFY AND DOKPLOY ARE NOT GUESSED. Both hand their user's compose file to
// real `docker compose`, so their files are indistinguishable from a plain one
// — and what tells them apart is which platform the person is leaving, which is
// not in the file. Guessing one would attach findings about an API nobody has.
func guessFrom(path string, data []byte) string {
	base := strings.ToLower(filepath.Base(path))

	switch {
	case base == "vercel.json":
		return vercel.Source
	case base == "fly.toml":
		return fly.Source
	case strings.HasPrefix(base, "compose.") || strings.HasPrefix(base, "docker-compose."):
		return compose.Source
	}

	// The content, for a file whose name says nothing. Each of these is a key
	// only one format has at the top level.
	if json.Valid(data) {
		return vercel.Source
	}

	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return ""
	}
	switch {
	case has(top, "services") && !has(top, "provider"):
		// `services` is compose's; Serverless Framework uses `service`.
		return compose.Source
	case has(top, "Resources"), has(top, "Transform"), has(top, "functions"), has(top, "provider"):
		return lambda.Source
	}
	return ""
}

func has(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func names() []string {
	out := make([]string, 0, len(sources))
	for name := range sources {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
