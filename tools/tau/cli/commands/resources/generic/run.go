package generic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/units"

	cliPrompts "github.com/taubyte/tau/pkg/cli/prompts"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/commands/build"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	runLib "github.com/taubyte/tau/tools/tau/lib/run"
	tauPrompts "github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

const runSeparator = "────────── running ──────────"

// Run executes a code-backed resource locally against an HTTP request. Only
// kinds that carry their own code have anything to run.
func (l link) Run() common.Command {
	if !l.code {
		return common.NotImplemented
	}
	return common.Create(
		&cli.Command{
			Action: l.runAction,
		},
		func(lk common.Linker) {
			lk.Flags().Push(runFlags()...)
		},
	)
}

func (l link) runAction(ctx *cli.Context) error {
	if err := projectLib.ConfirmSelectedProject(); err != nil {
		return err
	}

	name, doc, err := tcc.SelectResource(ctx, l.group.Dir)
	if err != nil {
		return err
	}
	fnSpec, err := runSpec(name, doc)
	if err != nil {
		return err
	}

	project, err := config.GetSelectedProject()
	if err != nil {
		return err
	}
	application, _ := config.GetSelectedApplication()

	wasmPath := ctx.String(RunWasm.Name)
	// Only auto-resolve from builds/, staleness check, and prompt for build when user did not set --wasm.
	if wasmPath == "" {
		projectConfig, err := projectLib.SelectedProjectConfig()
		if err != nil {
			return err
		}
		forceBuild := ctx.Bool(RunForceBuild.Name)
		if forceBuild {
			wasmPath, err = build.BuildFunctionToBuildsDir(projectConfig, application, name, os.Stderr)
			if err != nil {
				return err
			}
		} else {
			wasmPath, err = build.ResolveArtifactPath(projectConfig.Location, application, name)
			if err != nil {
				if cliPrompts.IsNonInteractive() {
					return err
				}
				if tauPrompts.ConfirmPrompt(ctx, "No WASM found. Build the function now?") {
					wasmPath, err = build.BuildFunctionToBuildsDir(projectConfig, application, name, os.Stderr)
					if err != nil {
						return err
					}
				} else {
					return err
				}
			} else {
				sourceDir := build.SourceDirForFunction(projectConfig.Location, application, name)
				stale, err := build.IsArtifactStale(wasmPath, sourceDir)
				if err != nil {
					return fmt.Errorf("checking artifact staleness: %w", err)
				}
				if stale {
					if cliPrompts.IsNonInteractive() {
						return fmt.Errorf("WASM is outdated (source changed). Rebuild with \"tau build function\" or use --force-build")
					}
					if tauPrompts.ConfirmPrompt(ctx, "WASM is outdated (source changed). Rebuild now?") {
						wasmPath, err = build.BuildFunctionToBuildsDir(projectConfig, application, name, os.Stderr)
						if err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if _, err := os.Stat(wasmPath); err != nil {
		return fmt.Errorf("wasm file %q: %w", wasmPath, err)
	}

	method := ctx.String(RunMethod.Name)
	if method == "" {
		method = fnSpec.Method
	}
	if method == "" {
		method = http.MethodGet
	}

	path := ctx.String(RunPath.Name)
	if path == "" && len(fnSpec.Paths) > 0 {
		path = fnSpec.Paths[0]
	}
	if path == "" {
		path = "/"
	}

	host := ctx.String(RunDomain.Name)
	if host == "" && len(fnSpec.Domains) > 0 {
		host = fnSpec.Domains[0]
	}
	if host == "" {
		host = "localhost"
	}

	var body io.Reader
	if b := ctx.String(RunBody.Name); b != "" {
		if strings.HasPrefix(b, "@") {
			f, err := os.Open(strings.TrimPrefix(b, "@"))
			if err != nil {
				return fmt.Errorf("body file: %w", err)
			}
			defer f.Close()
			body = f
		} else {
			body = bytes.NewBufferString(b)
		}
	}

	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+host+path, body)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	for _, h := range ctx.StringSlice(RunHeader.Name) {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	recorder := httptest.NewRecorder()

	runCtx := ctx.Context
	if d := ctx.Duration(RunTimeout.Name); d > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, d)
		defer cancel()
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, runSeparator)
	fmt.Fprintln(os.Stderr)

	if err := runLib.HttpFunction(runCtx, wasmPath, fnSpec, project, application, req, recorder); err != nil {
		return err
	}

	fmt.Printf("HTTP/1.1 %d %s\n", recorder.Code, http.StatusText(recorder.Code))
	for k, v := range recorder.Header() {
		for _, vv := range v {
			fmt.Printf("%s: %s\n", k, vv)
		}
	}
	fmt.Println()
	io.Copy(os.Stdout, recorder.Body)
	fmt.Println()
	return nil
}

// runSpec is the slice of a resource's document the local runner needs. Values
// come from the DSL's own paths, parsed with the same human forms the config
// uses ("30s", "32MB").
func runSpec(name string, doc tcc.Doc) (*structureSpec.Function, error) {
	trigger, _ := tcc.Get(doc, []string{"trigger", "type"}).(string)
	if trigger != "http" && trigger != "https" {
		return nil, fmt.Errorf("run is only supported for HTTP(S) triggers (got %q)", trigger)
	}

	spec := &structureSpec.Function{Name: name, Type: trigger}
	spec.Id, _ = tcc.Get(doc, []string{"id"}).(string)
	spec.Method, _ = tcc.Get(doc, []string{"trigger", "method"}).(string)
	spec.Paths = stringList(tcc.Get(doc, []string{"trigger", "paths"}))
	spec.Domains = stringList(tcc.Get(doc, []string{"trigger", "domains"}))
	spec.Call, _ = tcc.Get(doc, []string{"execution", "call"}).(string)

	if s, ok := tcc.Get(doc, []string{"execution", "memory"}).(string); ok && s != "" {
		size, err := units.ParseBase2Bytes(s)
		if err != nil {
			return nil, fmt.Errorf("memory %q: %w", s, err)
		}
		spec.Memory = uint64(size)
	}
	if s, ok := tcc.Get(doc, []string{"execution", "timeout"}).(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("timeout %q: %w", s, err)
		}
		spec.Timeout = uint64(d)
	}
	return spec, nil
}
