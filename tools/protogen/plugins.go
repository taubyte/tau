package protogen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// pluginPin ties a local buf plugin binary to the go.mod module whose
// required version its output is pinned to. Both of pkg/proto's local
// plugins live inside the module they generate code against — protoc-gen-go
// in google.golang.org/protobuf, protoc-gen-connect-go in
// connectrpc.com/connect — so the same go.mod requirement that pins the
// generated code's runtime dependency also names the version the generator
// itself is supposed to be.
type pluginPin struct {
	Binary string
	Module string
}

// pluginPins are pkg/proto/buf.gen.yaml's two local plugins.
//
// protoc-gen-go stamps its own version into every file it generates
// ("// protoc-gen-go vX.Y.Z"), so a version mismatch already surfaces as a
// byte-for-byte diff against the committed gen/ tree — Check's ordinary
// path catches it by accident.
//
// protoc-gen-connect-go does not stamp anything: it emits only a floor
// check, `connect.IsAtLeastVersion1_13_0`, no literal version anywhere in
// its output. Verified directly rather than assumed: a trivial proto and a
// proto exercising unary, server-stream, client-stream, bidi-stream and a
// deprecated RPC together produce byte-identical `buf generate` output
// under connect-go v1.18.1 and v1.19.1. So without checkPluginVersions, the
// v1.18.1 pin in buf.gen.yaml's comment is advisory only — nothing would
// notice a different version on PATH. checkPluginVersions asks the binary
// directly (`--version`) instead of inferring from output, which is the
// only mechanism that actually enforces it.
var pluginPins = []pluginPin{
	{Binary: "protoc-gen-go", Module: "google.golang.org/protobuf"},
	{Binary: "protoc-gen-connect-go", Module: "connectrpc.com/connect"},
}

// checkPluginVersions verifies that every plugin absDir's own buf.gen.yaml
// actually declares — restricted to the ones pluginPins knows how to pin —
// reports (via its own --version flag) the exact version go.mod requires
// for the module it is pinned to. Restricting to what buf.gen.yaml declares
// keeps this reusable across proto modules that use a different plugin set:
// a module that never lists protoc-gen-connect-go is never asked to have
// one on PATH.
//
// Called from Check before buf generate ever runs, so a wrong-version
// plugin fails with a message naming the plugin and the fix, rather than
// either silently generating something nobody asked for
// (protoc-gen-connect-go) or failing 60 files deep into a diff report that
// only shows a version-comment mismatch (protoc-gen-go).
func checkPluginVersions(repoRoot, absDir string) error {
	used, err := modulePlugins(absDir)
	if err != nil {
		return err
	}

	var pinned []pluginPin
	for _, pin := range pluginPins {
		if used[pin.Binary] {
			pinned = append(pinned, pin)
		}
	}
	if len(pinned) == 0 {
		return nil // this module uses none of the plugins pluginPins knows how to pin
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("parse go.mod: %w", err)
	}
	required := make(map[string]string, len(mf.Require))
	for _, r := range mf.Require {
		required[r.Mod.Path] = r.Mod.Version
	}

	for _, pin := range pinned {
		wantRaw, ok := required[pin.Module]
		if !ok {
			return fmt.Errorf("go.mod no longer requires %s, which pins %s's version — update pluginPins in tools/protogen/plugins.go", pin.Module, pin.Binary)
		}
		want := strings.TrimPrefix(wantRaw, "v")

		path, err := exec.LookPath(pin.Binary)
		if err != nil {
			return fmt.Errorf("%s not found on PATH (pinned to v%s by go.mod's %s requirement): %w", pin.Binary, want, pin.Module, err)
		}

		got, err := pluginVersion(path)
		if err != nil {
			return fmt.Errorf("%s --version: %w", pin.Binary, err)
		}

		if got != want {
			return fmt.Errorf("%s on PATH reports v%s, but go.mod requires %s v%s — go install %s/cmd/%s@v%s",
				pin.Binary, got, pin.Module, want, pin.Module, pin.Binary, want)
		}
	}
	return nil
}

// bufGenYAML is the slice of buf.gen.yaml's shape checkPluginVersions needs:
// which local plugins a module actually invokes.
type bufGenYAML struct {
	Plugins []struct {
		Local string `yaml:"local"`
	} `yaml:"plugins"`
}

// modulePlugins reads absDir/buf.gen.yaml and returns the set of `local:`
// plugin names it declares.
func modulePlugins(absDir string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(absDir, "buf.gen.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read buf.gen.yaml: %w", err)
	}
	var cfg bufGenYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse buf.gen.yaml: %w", err)
	}
	used := make(map[string]bool, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		if p.Local != "" {
			used[p.Local] = true
		}
	}
	return used, nil
}

// pluginVersion runs `bin --version` and extracts a bare version number.
// protoc-gen-go prints "protoc-gen-go vX.Y.Z"; protoc-gen-connect-go prints
// just "X.Y.Z" — both formats end in the version as the last
// whitespace-separated token, with an optional "v" prefix, so one parse
// handles both rather than special-casing per plugin.
func pluginVersion(bin string) (string, error) {
	var out, stderr bytes.Buffer
	cmd := exec.Command(bin, "--version")
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, stderr.String())
	}
	fields := strings.Fields(out.String())
	if len(fields) == 0 {
		return "", fmt.Errorf("empty --version output")
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v"), nil
}
