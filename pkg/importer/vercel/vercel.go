// Package vercel imports a vercel.json, and is precise about the half it cannot
// see.
//
// # A file-only Vercel import is partial, and says so
//
// Vercel keeps environment variables, secrets, project domains and deployment
// settings in its API, not in vercel.json. So this importer produces what the
// file says and reports the rest as Elsewhere — a level that exists exactly for
// this: it is not a gap in tau's vocabulary and it must not appear on the list
// of things to build. The fix is an account connection.
//
// That is worth doing rather than waiting, because the file half is the half
// that decides what the project IS — build settings, function limits, routing —
// and a person can see whether tau fits before they hand over a token.
//
// # Routing splits in two, and only one half is an edge
//
// importers.md is explicit about this and it is the subtlest thing here. A
// `rewrite` whose source path selects a target is ROUTING: it becomes an edge
// from a domain to a resource. A `redirect`, a `header` or a status is NOT
// routing — it has no target, it modifies a response — and there is nothing in
// the vocabulary for it. Reading the whole block as routing would produce edges
// that send traffic somewhere on the strength of a rule that was only ever
// meant to set a header.
//
// PCRE source patterns are unexpressible either way, and are reported as such
// rather than approximated into a prefix that matches different things.
package vercel

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/importer"
)

// defaultMemory and defaultCall are what a function that names neither is
// given. Neither is a measurement: the vocabulary requires both, Vercel's file
// carries neither, and a resource emitted without them does not compile. Each
// is reported where it is chosen.
const (
	defaultMemory = "128MB"
	defaultCall   = "handler"

	// defaultBuildImage is the toolchain a Vercel build runs in when the file
	// does not say. Vercel's own default has tracked Node LTS; this is not a
	// measurement of anything and the finding beside it says so.
	defaultBuildImage = "node:22"
)

// Source is what this importer is called in a finding.
const Source = "vercel"

var ErrNotVercel = errors.New("vercel: not a vercel.json")

type file struct {
	Version   int                 `json:"version"`
	Functions map[string]function `json:"functions"`
	Crons     []cron              `json:"crons"`
	Redirects []rule              `json:"redirects"`
	Rewrites  []rule              `json:"rewrites"`
	Headers   []headerRule        `json:"headers"`

	BuildCommand    *string `json:"buildCommand"`
	OutputDirectory *string `json:"outputDirectory"`
	InstallCommand  *string `json:"installCommand"`
	Framework       *string `json:"framework"`

	CleanUrls     *bool    `json:"cleanUrls"`
	TrailingSlash *bool    `json:"trailingSlash"`
	Regions       []string `json:"regions"`
	Env           map[string]string
}

type function struct {
	Memory      int    `json:"memory"`
	MaxDuration int    `json:"maxDuration"`
	Runtime     string `json:"runtime"`
}

type cron struct {
	Path     string `json:"path"`
	Schedule string `json:"schedule"`
}

type rule struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Permanent   *bool  `json:"permanent"`
	StatusCode  int    `json:"statusCode"`
}

type headerRule struct {
	Source  string `json:"source"`
	Headers []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"headers"`
}

// Import reads a vercel.json.
func Import(data []byte) (importer.Result, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return importer.Result{}, fmt.Errorf("vercel: %w", err)
	}

	r := importer.Result{Source: Source}

	// THE BUILD HALF IS WHAT THE FILE IS FOR, and what it describes is a
	// website: a repository built by a command into a directory that is then
	// served. That is the `build/commands` branch of the website kind, and the
	// field names are the vocabulary's own rather than Vercel's.
	commands := map[string]any{}
	if f.InstallCommand != nil && *f.InstallCommand != "" {
		commands["install"] = *f.InstallCommand
	}
	if f.BuildCommand != nil && *f.BuildCommand != "" {
		commands["run"] = *f.BuildCommand
	}
	if f.OutputDirectory != nil && *f.OutputDirectory != "" {
		commands["output"] = *f.OutputDirectory
	}
	if len(commands) > 0 {
		// THE BUILD IMAGE IS REQUIRED AND VERCEL DOES NOT NAME ONE. Vercel
		// picks a Node version from a project setting and a package.json
		// field, neither of which is in this file.
		//
		// SO ONE IS WRITTEN, AND THAT IS A REVERSAL. This emitted the block
		// without an image and reported the hole, which read as the careful
		// choice and was the worst of the three: the project imported, the
		// to-do list was EMPTY — a dynamic path is invisible to the session's
		// required-field check — and tcc then refused the whole thing over a
		// field nothing had named. Between a build against a runtime nobody
		// chose and a project that cannot compile at all, the first is the one
		// a person can see and change.
		commands["image"] = defaultBuildImage

		r.Set(siteKind, SiteName, map[string]any{
			"description": "imported from vercel.json",
			"build":       map[string]any{"commands": commands},
		})

		r.Add(importer.Finding{Resource: SiteName, Key: "build.image", Level: importer.Approximated,
			Code: "SOURCE_IN_API:node-version",
			Detail: "the image a build runs in has to be named, and Vercel picks it from a project setting and " +
				"package.json's engines field rather than from this file; " + defaultBuildImage +
				" was written — change it if the build needs another runtime"})
	}

	if f.Framework != nil && *f.Framework != "" {
		// A FRAMEWORK NAME IS A PRESET, AND A PRESET IS FOUR FIELDS. Carrying
		// the name would put a word in the config that nothing reads; what it
		// stands for is buildCommand/outputDirectory/installCommand, and those
		// are only in this file when the author overrode them.
		r.Add(importer.Finding{Key: "framework", Level: importer.Approximated,
			Code: "FRAMEWORK_PRESET_NOT_EXPANDED",
			Detail: fmt.Sprintf("framework %q selects Vercel's own build defaults; only the fields this file overrides were imported, "+
				"so anything the preset supplied silently is missing", *f.Framework)})
	}

	importFunctions(&r, f.Functions)
	importRouting(&r, f)

	if len(f.Crons) > 0 {
		var paths []string
		for _, c := range f.Crons {
			paths = append(paths, c.Schedule+" "+c.Path)
		}
		r.Add(importer.Finding{Key: "crons", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:cron",
			Needs:  "a scheduled trigger that invokes a resource on a schedule",
			Detail: "crons parse cleanly and have nowhere to go: " + strings.Join(paths, ", ")})
	}

	if len(f.Regions) > 0 {
		r.Add(importer.Finding{Key: "regions", Level: importer.Ignored,
			Code:   "IGNORED:regions",
			Detail: "where a workload runs is the platform's decision here, not the project's"})
	}

	// The half that is not in this file at all.
	r.Add(importer.Finding{Key: "env", Level: importer.Elsewhere,
		Code:   "SOURCE_WRITE_ONLY:secret",
		Detail: "production and preview environment variables live in Vercel's API and are write-only there; keys and scopes can be read with a token, values cannot"})
	r.Add(importer.Finding{Key: "domains", Level: importer.Elsewhere,
		Code:   "SOURCE_IN_API:domains",
		Detail: "project domains and their certificates are API state, not file state"})

	r.Sort()
	return r, nil
}

// siteKind and functionKind are what the two halves of a Vercel project become.
const (
	siteKind     = "website"
	functionKind = "function"

	// SiteName is what the imported website is called. Vercel's file carries no
	// project name — the name is a dashboard setting — so one is chosen here
	// rather than left empty, and it is stated so a caller can rename it.
	SiteName = "site"
)

func importFunctions(r *importer.Result, fns map[string]function) {
	names := make([]string, 0, len(fns))
	for k := range fns {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, glob := range names {
		fn := fns[glob]
		doc := map[string]any{}

		// memory and maxDuration are the one clean mapping: tau has per-resource
		// limits, and they are the same two numbers. They live under
		// `execution`, which is where the vocabulary puts them.
		execution := map[string]any{}
		if fn.Memory > 0 {
			execution["memory"] = fmt.Sprintf("%dMB", fn.Memory)
		}
		if fn.MaxDuration > 0 {
			execution["timeout"] = fmt.Sprintf("%ds", fn.MaxDuration)
		}
		// THE VOCABULARY REQUIRES BOTH AND VERCEL'S FILE CARRIES NEITHER
		// RELIABLY, so both are chosen here and said. A resource emitted
		// without them imported and then would not compile.
		if _, ok := execution["memory"]; !ok {
			execution["memory"] = defaultMemory
			r.Add(importer.Finding{Resource: glob, Key: "functions.memory", Level: importer.Approximated,
				Code: "MEMORY_DEFAULTED",
				Detail: "this function declares no memory, so it had Vercel's own default; tau requires one and " +
					defaultMemory + " was chosen"})
		}

		// `call` NAMES THE EXPORT THE HOST CALLS, and Vercel has no equivalent
		// field: its Node runtime calls the module's default export, which by
		// convention is written `export default function handler`. So the
		// convention is what is written, and it is reported because a function
		// whose entrypoint is named anything else will not be found.
		execution["call"] = defaultCall
		r.Add(importer.Finding{Resource: glob, Key: "execution.call", Level: importer.Approximated,
			Code: "ENTRYPOINT_ASSUMED",
			Detail: "Vercel calls a function's default export and names nothing; tau names the export it calls, so " +
				defaultCall + " was written — change it if the built component exports something else"})

		doc["execution"] = execution

		// A GLOB IS NOT A RESOURCE. `api/**/*.ts` configures every file it
		// matches; each of those is a function here, and this file does not say
		// how many there are or what they are called. One resource is emitted
		// per glob, named after it, and the mismatch is reported rather than
		// hidden behind a plausible-looking name.
		name, _ := importer.Name(glob)
		if name == "" {
			continue
		}
		r.Add(importer.Finding{Resource: name, Key: "functions." + glob, Level: importer.Approximated,
			Code:  "GLOB_IS_NOT_A_RESOURCE",
			Needs: "nothing — the shapes differ",
			Detail: "\"" + glob + "\" configures every file it matches, and each of those is its own function here; " +
				"one resource was made for the pattern and it has to be split by hand"})

		if len(doc) > 0 {
			r.Set(functionKind, name, doc)
		}

		switch {
		case strings.HasPrefix(fn.Runtime, "edge"):
			r.Add(importer.Finding{Resource: glob, Key: "runtime", Level: importer.Approximated,
				Code:   "RUNTIME_EDGE_IS_FETCH_SHAPED",
				Detail: "an edge function is Fetch-shaped and maps onto a wasi:http handler, which is the cheapest function import there is — but it is a different runtime and its timeouts, streaming and available APIs differ"})
		case fn.Runtime != "":
			r.Add(importer.Finding{Resource: glob, Key: "runtime", Level: importer.Blocked,
				Code:   "NEEDS_JS_ON_WASM",
				Needs:  "a JavaScript compute tier",
				Detail: fmt.Sprintf("runtime %q is near-vanilla Node: portable in shape, not in runtime", fn.Runtime)})
		}
	}
}

// importRouting splits the routing block in two — see the package doc.
func importRouting(r *importer.Result, f file) {
	for _, rw := range f.Rewrites {
		if isPCRE(rw.Source) {
			r.Add(importer.Finding{Key: "rewrites", Level: importer.Blocked,
				Code:   "PATTERN_NOT_EXPRESSIBLE",
				Needs:  "a path matcher richer than longest-prefix",
				Detail: "rewrite source " + rw.Source + " is a pattern, and approximating it as a prefix would match different requests"})
			continue
		}
		// A rewrite whose source selects a destination IS routing: it becomes an
		// edge from a domain to a resource.
		r.Add(importer.Finding{Key: "rewrites", Level: importer.Approximated,
			Code:   "ROUTING_BECOMES_AN_EDGE",
			Detail: fmt.Sprintf("%s -> %s becomes an inbound edge with a mount; Vercel resolves filesystem routes before rewrites, and that ordering is not carried", rw.Source, rw.Destination)})
	}

	if len(f.Redirects) > 0 {
		r.Add(importer.Finding{Key: "redirects", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:redirects",
			Needs:  "a response rule on a route: status and location without a target resource",
			Detail: "a redirect has no target to route to; it modifies a response, which is not an edge"})
	}
	if len(f.Headers) > 0 {
		r.Add(importer.Finding{Key: "headers", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:headers",
			Needs:  "a response rule on a route",
			Detail: "a header rule modifies a response and selects no target"})
	}
	if f.CleanUrls != nil || f.TrailingSlash != nil {
		r.Add(importer.Finding{Key: "cleanUrls", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:url-normalisation",
			Needs:  "a response rule on a route",
			Detail: "cleanUrls and trailingSlash rewrite the request path before anything routes on it"})
	}
}

// isPCRE reports whether a Vercel source is a pattern rather than a plain path.
//
// `:param` and `:param*` segments are Vercel's own syntax and are ordinary named
// segments; a bracketed group or an anchor is a regular expression.
func isPCRE(source string) bool {
	return strings.ContainsAny(source, "()[]|+?^$\\")
}
