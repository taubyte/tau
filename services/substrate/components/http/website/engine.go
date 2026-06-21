package website

import (
	"fmt"
	goHttp "net/http"
	"sort"
	"strings"
	"time"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// renderFunc renders an SSR request through a server bundle of a particular
// handler ABI.
type renderFunc func(*Website, goHttp.ResponseWriter, *goHttp.Request) (time.Time, error)

// ssrEngines maps a handler ABI to the engine that runs it. This is the seam
// for adding JavaScript-runtime backends: the `function` and `wasi-stdio`
// engines are the wazero-backed reference implementations (proven end to end);
// a richer component-model engine (StarlingMonkey) registers ABIComponent here
// once a component-model runtime backend exists in the VM layer. See
// docs/js-runtime-roadmap.md.
var ssrEngines = map[string]renderFunc{
	websiteSpec.ABIFunction:  (*Website).serveSSRFunction,
	websiteSpec.ABIWasiStdio: (*Website).serveSSRStdio,
}

// serveSSR dispatches the request to the engine registered for the manifest's
// handler ABI, failing fast (with the set this build supports) when none is.
func (w *Website) serveSSR(_w goHttp.ResponseWriter, r *goHttp.Request) (time.Time, error) {
	if render, ok := ssrEngines[w.ssr.ABIOrDefault()]; ok {
		return render(w, _w, r)
	}
	return time.Time{}, fmt.Errorf(
		"website `%s`: handler abi `%s` is not supported by this substrate build (supports: %s)",
		w.config.Name, w.ssr.ABIOrDefault(), strings.Join(supportedSSRABIs(), ", "),
	)
}

// supportedSSRABIs lists the handler ABIs this build can serve, sorted.
func supportedSSRABIs() []string {
	out := make([]string, 0, len(ssrEngines))
	for abi := range ssrEngines {
		out = append(out, abi)
	}
	sort.Strings(out)
	return out
}
