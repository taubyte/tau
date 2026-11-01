// Package lambda reads an AWS SAM template or serverless.yml and says, per function,
// what it would take to run it here. A Lambda handler is `(event, context) -> result`
// with a per-trigger event shape, not request-in, response-out, so this emits config
// for what maps and a finding for every part needing the handler rewritten; it never
// claims a Lambda runs unchanged. SnapStart is flagged: tau has no snapshot tier.
package lambda

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/importer"
)

// defaultMemory is AWS's own default, which is what a function naming no
// MemorySize actually ran under — not a number chosen here.
const defaultMemory = "128MB"

// Source is what this importer is called in a finding.
const Source = "lambda"

// file covers both dialects. SAM is CloudFormation with Resources; the
// Serverless Framework has a functions map. They are read together because a
// caller has one file and should not have to know which parser to reach for.
type file struct {
	// SAM / CloudFormation
	Transform any               `yaml:"Transform"`
	Globals   *globals          `yaml:"Globals"`
	Resources map[string]samRes `yaml:"Resources"`

	// Serverless Framework
	Service   any                    `yaml:"service"`
	Provider  *provider              `yaml:"provider"`
	Functions map[string]slsFunction `yaml:"functions"`
	Plugins   []string               `yaml:"plugins"`
}

type globals struct {
	Function *samProps `yaml:"Function"`
}

type samRes struct {
	Type       string    `yaml:"Type"`
	Properties *samProps `yaml:"Properties"`
}

type samProps struct {
	Handler       string            `yaml:"Handler"`
	Runtime       string            `yaml:"Runtime"`
	MemorySize    int               `yaml:"MemorySize"`
	Timeout       int               `yaml:"Timeout"`
	Architectures []string          `yaml:"Architectures"`
	Layers        []any             `yaml:"Layers"`
	VpcConfig     map[string]any    `yaml:"VpcConfig"`
	SnapStart     map[string]any    `yaml:"SnapStart"`
	Environment   *samEnv           `yaml:"Environment"`
	Events        map[string]samEvt `yaml:"Events"`
	Policies      any               `yaml:"Policies"`
	ImageUri      string            `yaml:"ImageUri"`
	PackageType   string            `yaml:"PackageType"`
}

type samEnv struct {
	Variables map[string]any `yaml:"Variables"`
}

type samEvt struct {
	Type string `yaml:"Type"`
}

type provider struct {
	Runtime     string         `yaml:"runtime"`
	MemorySize  int            `yaml:"memorySize"`
	Timeout     int            `yaml:"timeout"`
	Environment map[string]any `yaml:"environment"`
	VPC         map[string]any `yaml:"vpc"`
}

type slsFunction struct {
	Handler     string           `yaml:"handler"`
	Runtime     string           `yaml:"runtime"`
	MemorySize  int              `yaml:"memorySize"`
	Timeout     int              `yaml:"timeout"`
	Environment map[string]any   `yaml:"environment"`
	Layers      []any            `yaml:"layers"`
	VPC         map[string]any   `yaml:"vpc"`
	SnapStart   *bool            `yaml:"snapStart"`
	Image       any              `yaml:"image"`
	Events      []map[string]any `yaml:"events"`
}

// fn is one function, whichever dialect it came from.
type fn struct {
	name      string
	handler   string
	runtime   string
	memory    int
	timeout   int
	env       map[string]any
	events    []string
	layers    int
	vpc       bool
	snapStart bool
	container bool
	// image is the container image, when the function is packaged as one. It
	// was read and dropped before: a resource emitted without it lost the one
	// thing the file said about what actually runs.
	image string
}

func Import(data []byte) (importer.Result, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return importer.Result{}, fmt.Errorf("lambda: %w", err)
	}

	fns := collect(f)
	if len(fns) == 0 {
		return importer.Result{}, fmt.Errorf("lambda: no functions in this file")
	}

	r := importer.Result{Source: Source}

	// The handler shape is reported once, not per function.
	r.Add(importer.Finding{Key: "handler", Level: importer.Blocked,
		Code:  "HANDLER_NOT_FETCH_SHAPED",
		Needs: "a handler rewrite, or a compatibility layer that is somebody's own project",
		Detail: "a Lambda handler is (event, context) -> result with a different JSON event shape per trigger; " +
			"tau hands a workload a request and takes a response, so the code does not move unchanged"})

	for _, x := range fns {
		importFunction(&r, x)
	}

	if len(f.Plugins) > 0 {
		r.Add(importer.Finding{Key: "plugins", Level: importer.Blocked,
			Code:  "SOURCE_NOT_DATA",
			Needs: "",
			Detail: "serverless plugins are arbitrary code that rewrites the deployment at package time (" +
				strings.Join(f.Plugins, ", ") + "); what they produce cannot be read from this file"})
	}

	r.Add(importer.Finding{Key: "iam", Level: importer.Elsewhere,
		Code: "SOURCE_IN_API:iam",
		Detail: "a function's permissions are IAM roles in the account, and what it may reach is defined there " +
			"rather than in this file"})

	r.Sort()
	return r, nil
}

// collect reads both dialects into one shape.
func collect(f file) []fn {
	var out []fn

	// SAM: Resources of type AWS::Serverless::Function, with Globals as the
	// default for anything a function does not set.
	var g samProps
	if f.Globals != nil && f.Globals.Function != nil {
		g = *f.Globals.Function
	}
	for _, name := range sortedKeys(f.Resources) {
		res := f.Resources[name]
		if !strings.Contains(res.Type, "Serverless::Function") && !strings.Contains(res.Type, "Lambda::Function") {
			continue
		}
		p := res.Properties
		if p == nil {
			p = &samProps{}
		}
		x := fn{
			name:      name,
			handler:   or(p.Handler, g.Handler),
			runtime:   or(p.Runtime, g.Runtime),
			memory:    orInt(p.MemorySize, g.MemorySize),
			timeout:   orInt(p.Timeout, g.Timeout),
			layers:    len(p.Layers) + len(g.Layers),
			vpc:       p.VpcConfig != nil || g.VpcConfig != nil,
			snapStart: p.SnapStart != nil || g.SnapStart != nil,
			container: p.PackageType == "Image" || p.ImageUri != "",
			image:     p.ImageUri,
		}
		if p.Environment != nil {
			x.env = p.Environment.Variables
		} else if g.Environment != nil {
			x.env = g.Environment.Variables
		}
		for _, e := range sortedKeys(p.Events) {
			x.events = append(x.events, p.Events[e].Type)
		}
		out = append(out, x)
	}

	// Serverless Framework: a functions map, with provider as the default.
	var pr provider
	if f.Provider != nil {
		pr = *f.Provider
	}
	for _, name := range sortedKeys(f.Functions) {
		s := f.Functions[name]
		x := fn{
			name:      name,
			handler:   s.Handler,
			runtime:   or(s.Runtime, pr.Runtime),
			memory:    orInt(s.MemorySize, pr.MemorySize),
			timeout:   orInt(s.Timeout, pr.Timeout),
			env:       s.Environment,
			layers:    len(s.Layers),
			vpc:       s.VPC != nil || pr.VPC != nil,
			snapStart: s.SnapStart != nil && *s.SnapStart,
			container: s.Image != nil,
			image:     imageOf(s.Image),
		}
		if x.env == nil {
			x.env = pr.Environment
		}
		for _, e := range s.Events {
			for _, k := range sortedKeys(e) {
				x.events = append(x.events, k)
			}
		}
		out = append(out, x)
	}
	return out
}

// A zip-packaged Lambda becomes a `function` (wasm); a container-packaged one an
// `application` (the only kind starting from an image).
const (
	functionKind  = "function"
	containerKind = "application"
)

// imageOf reads the Serverless Framework's `image:`, which is a string or a
// block with a `uri`.
func imageOf(v any) string {
	switch image := v.(type) {
	case string:
		return image
	case map[string]any:
		uri, _ := image["uri"].(string)
		return uri
	}
	return ""
}

func importFunction(r *importer.Result, x fn) {
	name, renamed := importer.Name(x.name)
	if name == "" {
		return
	}
	if renamed {
		r.Add(importer.Renamed(name, "functions", x.name, name))
	}

	doc := map[string]any{}

	// The two clean rows: tau has per-resource limits and they are the same
	// numbers. They live under `execution`, which is where the vocabulary puts
	// them.
	execution := map[string]any{}
	if x.memory > 0 {
		execution["memory"] = fmt.Sprintf("%dMB", x.memory)
	}
	if x.timeout > 0 {
		execution["timeout"] = fmt.Sprintf("%ds", x.timeout)
	}
	if len(execution) > 0 {
		doc["execution"] = execution
	}
	if len(x.env) > 0 {
		variables := map[string]any{}
		for k, v := range x.env {
			// A CloudFormation intrinsic is not a value. !Ref, !GetAtt and
			// !Sub resolve against a stack that will not exist here, so
			// carrying one would put a dangling reference in the config.
			if isIntrinsic(v) {
				r.Add(importer.Finding{Resource: name, Key: "environment." + k,
					Level: importer.Blocked, Code: "CLOUDFORMATION_INTRINSIC",
					Needs:  "",
					Detail: "the value is a CloudFormation reference resolved against a stack that does not exist here"})
				continue
			}
			// An environment variable is a string. A structured value is not
			// one, and stringifying it would emit a Go debug rendering that
			// the process would read as a real value.
			t, ok := scalar(v)
			if !ok {
				r.Add(importer.Finding{Resource: name, Key: "environment." + k,
					Level: importer.Blocked, Code: "ENV_NOT_A_SCALAR",
					Detail: "the value is a structure, and an environment variable can only carry a string"})
				continue
			}
			stem, renamedKey := importer.Name(k)
			if stem == "" {
				continue
			}
			if renamedKey {
				r.Add(importer.Renamed(name, "environment."+k, k, stem))
			}
			// A variable is a block; `at` and `as` are defaulted.
			variables[stem] = map[string]any{"value": t}
		}
		if len(variables) > 0 {
			doc["variables"] = variables
		}
	}
	kind := functionKind
	if x.container {
		kind = containerKind
		if x.image != "" {
			doc["source"] = map[string]any{"image": x.image}
		}

		// An application is not invoked per request and has no timeout, so a Lambda's Timeout
		// is dropped and reported.
		if execution, ok := doc["execution"].(map[string]any); ok {
			if _, had := execution["timeout"]; had {
				delete(execution, "timeout")
				r.Add(importer.Finding{Resource: name, Key: "Timeout", Level: importer.Blocked,
					Code:  "NO_TAU_FEATURE:container-timeout",
					Needs: "a per-invocation ceiling on the container tier",
					Detail: fmt.Sprintf("this function is bounded at %ds; a container-packaged one becomes an "+
						"application here, which has no per-request timeout, so nothing stops a run that hangs", x.timeout)})
			}
		}
		// AN application NEEDS A CEILING AND AWS'S DEFAULT IS 128MB, which is
		// the number a Lambda that names none actually ran under.
		if x.memory == 0 {
			execution := doc["execution"]
			if execution == nil {
				execution = map[string]any{}
				doc["execution"] = execution
			}
			execution.(map[string]any)["memory"] = defaultMemory
			r.Add(importer.Finding{Resource: name, Key: "MemorySize", Level: importer.Approximated,
				Code:   "MEMORY_DEFAULTED",
				Detail: "this function names no MemorySize, so it ran under AWS's default; " + defaultMemory + " was written"})
		}
	}
	if len(doc) > 0 {
		r.Set(kind, name, doc)
	}

	if x.container {
		// The container tier serves the Lambda Runtime API on loopback (AWS_LAMBDA_RUNTIME_API),
		// so the image runs unmodified; the event shape still differs per trigger.
		r.Add(importer.Finding{Resource: name, Key: "image", Level: importer.Approximated,
			Code: "IMAGE_INVOKED_OVER_HTTP",
			Detail: "this image polls the Lambda Runtime API, which the container tier serves — so it runs " +
				"unmodified. It is invoked over HTTP and the event is API Gateway payload format 2.0, the shape " +
				"a Function URL delivers; a handler written for an S3, SQS or EventBridge event will not " +
				"recognise it. There is no extensions API and no function ARN"})
	}

	if x.snapStart {
		r.Add(importer.Finding{Resource: name, Key: "snapStart", Level: importer.Approximated,
			Code: "SNAPSTART_HAS_NO_EQUIVALENT",
			Detail: "this function is tuned around SnapStart; tau has no snapshot tier, so its cold-start profile " +
				"changes by an order of magnitude and nothing else in the manifest reveals that"})
	}
	if x.layers > 0 {
		r.Add(importer.Finding{Resource: name, Key: "layers", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:layers",
			Needs:  "a shared dependency artifact mounted into a build",
			Detail: fmt.Sprintf("%d layer(s) supply code this function expects at runtime and are not in the repository", x.layers)})
	}
	if x.vpc {
		r.Add(importer.Finding{Resource: name, Key: "vpc", Level: importer.Blocked,
			Code:   "NO_TAU_FEATURE:vpc",
			Needs:  "a private network the workload joins",
			Detail: "the function reaches private resources through a VPC; tau's egress policy is a different model"})
	}

	importEvents(r, x)
}

// importEvents says what each trigger would need. A Lambda's event shape is per
// trigger, so this is also where "not Fetch-shaped" becomes concrete.
func importEvents(r *importer.Result, x fn) {
	name, _ := importer.Name(x.name)
	seen := map[string]bool{}
	for _, e := range x.events {
		if seen[e] {
			continue
		}
		seen[e] = true

		switch strings.ToLower(e) {
		case "api", "httpapi", "http":
			r.Add(importer.Finding{Resource: name, Key: "events." + e,
				Level: importer.Approximated, Code: "ROUTING_BECOMES_AN_EDGE",
				Detail: "an HTTP trigger becomes an inbound edge; the handler still receives an API Gateway event " +
					"rather than a request, so the code changes even though the routing carries"})
		case "schedule":
			r.Add(importer.Finding{Resource: name, Key: "events." + e,
				Level: importer.Blocked, Code: "NO_TAU_FEATURE:cron",
				Needs:  "a scheduled trigger",
				Detail: "a scheduled invocation has nowhere to be declared"})
		default:
			r.Add(importer.Finding{Resource: name, Key: "events." + e,
				Level: importer.Blocked, Code: "NO_TAU_FEATURE:event-source",
				Needs:  "an inbound edge from the corresponding tau resource",
				Detail: fmt.Sprintf("a %s trigger delivers an AWS-shaped event from an AWS service; both the source and the payload would have to be replaced", e)})
		}
	}
}

// isIntrinsic reports whether a YAML value is a CloudFormation intrinsic rather
// than a literal — !Ref, !GetAtt, !Sub and the Fn:: forms.
func isIntrinsic(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k := range m {
		if k == "Ref" || strings.HasPrefix(k, "Fn::") {
			return true
		}
	}
	return false
}

// scalar renders a YAML value as the string an environment variable can carry,
// and refuses anything that is not one.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int, int64, float64, bool:
		return fmt.Sprint(t), true
	case nil:
		return "", true
	default:
		return "", false
	}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func orInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
