package frameworks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	commonSpec "github.com/taubyte/tau/pkg/specs/builders/common"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"gopkg.in/yaml.v3"
)

// BuildScript is the name (without extension) of the generated build step.
const BuildScript = "build"

// Generated holds everything needed to build a framework with zero manual
// configuration: a `.taubyte/config.yaml`, the build scripts it references, and
// the SSR manifest the runtime will consume.
type Generated struct {
	Config   string
	Scripts  map[string]string
	Manifest *websiteSpec.Manifest
}

type genConfig struct {
	Version     string `yaml:"version"`
	Environment struct {
		Image string `yaml:"image"`
	} `yaml:"environment"`
	Workflow []string `yaml:"workflow"`
}

// Generate produces the build configuration, build script and SSR manifest for
// a framework. The result is what `Materialize` writes into a repository that
// ships no `.taubyte` configuration of its own.
func Generate(f *Framework) (*Generated, error) {
	cfg := genConfig{Version: "1", Workflow: []string{BuildScript}}
	cfg.Environment.Image = f.Image

	cfgYAML, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding build config failed with: %w", err)
	}

	manifest := f.Manifest()

	var script string
	if f.IsSSR() {
		script = ssrScript(f)
	} else {
		script = staticScript(f)
	}

	return &Generated{
		Config:   string(cfgYAML),
		Scripts:  map[string]string{BuildScript + commonSpec.ScriptExtension: script},
		Manifest: manifest,
	}, nil
}

// Materialize writes the generated configuration into dir/.taubyte, creating it
// when needed. It never overwrites an existing config so a hand written
// `.taubyte` always wins.
func Materialize(dir string, g *Generated) error {
	taubyteDir := filepath.Join(dir, commonSpec.TaubyteDir)
	if _, err := os.Stat(filepath.Join(taubyteDir, commonSpec.ConfigFile)); err == nil {
		return fmt.Errorf("refusing to overwrite existing `%s`", filepath.Join(commonSpec.TaubyteDir, commonSpec.ConfigFile))
	}

	if err := os.MkdirAll(taubyteDir, 0o755); err != nil {
		return fmt.Errorf("creating `%s` failed with: %w", taubyteDir, err)
	}

	if err := os.WriteFile(filepath.Join(taubyteDir, commonSpec.ConfigFile), []byte(g.Config), 0o644); err != nil {
		return fmt.Errorf("writing build config failed with: %w", err)
	}

	for name, content := range g.Scripts {
		if err := os.WriteFile(filepath.Join(taubyteDir, name), []byte(content), 0o755); err != nil {
			return fmt.Errorf("writing build script `%s` failed with: %w", name, err)
		}
	}

	return nil
}

// staticScript builds a static / SPA / SSG framework: install, build, publish
// the static output directory.
func staticScript(f *Framework) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(fmt.Sprintf("# Auto-generated Taubyte build for %s (static)\n", f.Title))
	b.WriteString("set -e\n")
	b.WriteString(`cd "$SRC"` + "\n")
	writeCmd(&b, f.Install)
	writeCmd(&b, f.Build)
	b.WriteString(`mkdir -p "$OUT"` + "\n")
	b.WriteString(fmt.Sprintf("cp -r %s/. \"$OUT\"/\n", shellQuote(f.StaticDir)))
	return b.String()
}

// ssrScript builds a server side rendered framework: install, build, publish
// static assets, then compile the server bundle to WebAssembly via the SSR
// adapter, which emits both the handler and the manifest the runtime reads.
func ssrScript(f *Framework) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(fmt.Sprintf("# Auto-generated Taubyte build for %s (ssr)\n", f.Title))
	b.WriteString("set -e\n")
	b.WriteString(`cd "$SRC"` + "\n")
	writeCmd(&b, f.Install)
	writeCmd(&b, f.Build)
	b.WriteString(`mkdir -p "$OUT" "$OUT/` + websiteSpec.ManifestDir() + `"` + "\n")
	if f.StaticDir != "" {
		b.WriteString("# publish immutable assets so they are served directly, never re-rendered\n")
		b.WriteString(fmt.Sprintf("cp -r %s/. \"$OUT\"/ 2>/dev/null || true\n", shellQuote(f.StaticDir)))
	}
	b.WriteString("# compile the server bundle to WebAssembly (handler + manifest). The\n")
	b.WriteString("# adapter is provided by the build image; override it with TAUBYTE_SSR_ADAPTER.\n")
	b.WriteString(`: "${TAUBYTE_SSR_ADAPTER:=taubyte-ssr-adapter}` + "\"\n")
	b.WriteString(fmt.Sprintf("\"$TAUBYTE_SSR_ADAPTER\" --framework %s --entry %s --out \"$OUT/%s\" --manifest \"$OUT/%s\"\n",
		shellQuote(f.Name), shellQuote(f.ServerEntry), websiteSpec.DefaultHandlerPath, websiteSpec.ManifestPath))
	return b.String()
}

func writeCmd(b *strings.Builder, cmd string) {
	if strings.TrimSpace(cmd) == "" {
		return
	}
	b.WriteString(cmd)
	b.WriteString("\n")
}

// shellQuote single-quotes a value for safe interpolation into the script.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
