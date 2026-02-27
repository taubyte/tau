package function

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/taubyte/tau/tools/tau/cli/commands/build"
	"github.com/taubyte/tau/tools/tau/cli/common"
	tauCommon "github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	runLib "github.com/taubyte/tau/tools/tau/lib/run"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	"github.com/urfave/cli/v2"
)

func (link) Run() common.Command {
	return common.Create(
		&cli.Command{
			Action: runAction,
		},
		func(l common.Linker) {
			l.Flags().Push(functionFlags.RunFlags()...)
		},
	)
}

func runAction(ctx *cli.Context) error {
	if err := projectLib.ConfirmSelectedProject(); err != nil {
		return err
	}

	fnSpec, err := functionPrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	if fnSpec.Type != tauCommon.FunctionTypeHttp && fnSpec.Type != tauCommon.FunctionTypeHttps {
		return fmt.Errorf("run is only supported for HTTP(S) functions (got %q)", fnSpec.Type)
	}

	project, err := config.GetSelectedProject()
	if err != nil {
		return err
	}
	application, _ := config.GetSelectedApplication()

	wasmPath := ctx.String(functionFlags.RunWasm.Name)
	if wasmPath == "" {
		projectConfig, err := projectLib.SelectedProjectConfig()
		if err != nil {
			return err
		}
		wasmPath, err = build.ResolveArtifactPath(projectConfig.Location, application, fnSpec.Name)
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(wasmPath); err != nil {
		return fmt.Errorf("wasm file %q: %w", wasmPath, err)
	}

	method := ctx.String(functionFlags.RunMethod.Name)
	if method == "" {
		method = fnSpec.Method
	}
	if method == "" {
		method = http.MethodGet
	}

	path := ctx.String(functionFlags.RunPath.Name)
	if path == "" && len(fnSpec.Paths) > 0 {
		path = fnSpec.Paths[0]
	}
	if path == "" {
		path = "/"
	}

	host := ctx.String(functionFlags.RunDomain.Name)
	if host == "" && len(fnSpec.Domains) > 0 {
		host = fnSpec.Domains[0]
	}
	if host == "" {
		host = "localhost"
	}

	var body io.Reader
	if b := ctx.String(functionFlags.RunBody.Name); b != "" {
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
	for _, h := range ctx.StringSlice(functionFlags.RunHeader.Name) {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	recorder := httptest.NewRecorder()

	runCtx := ctx.Context
	if d := ctx.Duration(functionFlags.RunTimeout.Name); d > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, d)
		defer cancel()
	}

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
	return nil
}
