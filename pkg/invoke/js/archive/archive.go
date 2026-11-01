// Package archive is the artifact a JS function ships as: a zip of the bundle, its
// chunks, source maps and any compiled `.warp` forms, with a manifest naming the
// entry module and the engine that compiled. The source always ships beside a
// compiled form, since a `.warp` loads only on the engine that wrote it. The cache
// key is the warpjs module version plus engine.BytecodeVersion; a "(devel)" build
// identifies no code.
package archive

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"runtime/debug"
	"sort"
	"strings"

	"warpjs/engine"
)

const (
	// ManifestName is the member that says what the rest of the archive is.
	ManifestName = "manifest.json"

	// Format is the version of this layout. A reader refuses one it does not
	// know rather than guessing at it.
	Format = 1

	// CompiledExt marks a compiled form: "<name>.warp" is the member <name>
	// compiled by the engine the manifest names.
	CompiledExt = ".warp"

	// DefaultMax bounds an archive, packed and unpacked: what the tier reads
	// unless told otherwise, and so what a build packs at most.
	DefaultMax = 64 << 20

	// modulePath is the engine's module path in a binary's build info.
	modulePath = "warpjs"
)

var (
	ErrBadName       = errors.New("archive: a member name must be a clean relative path")
	ErrNoEntry       = errors.New("archive: the entry is not one of the files")
	ErrCompiledEntry = errors.New("archive: the entry must be a source module, not a compiled form")
	ErrOrphanCompile = errors.New("archive: a compiled form must ship beside its source")
)

// Manifest is what manifest.json holds.
type Manifest struct {
	Format int    `json:"format"`
	Entry  string `json:"entry"`
	Engine Engine `json:"engine"`
}

// Engine is what compiled an archive's `.warp` members.
type Engine struct {
	Warpjs   string `json:"warpjs"`
	Bytecode string `json:"bytecode"`
}

// Current is the engine this binary runs.
func Current() Engine {
	return Engine{Warpjs: warpjsVersion(), Bytecode: engine.BytecodeVersion()}
}

func warpjsVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return versionIn(info.Deps)
	}
	return ""
}

func versionIn(deps []*debug.Module) string {
	for _, dep := range deps {
		if dep.Path != modulePath {
			continue
		}
		if dep.Replace != nil {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return ""
}

// CheckName refuses a member name that is not a clean, relative, slash-separated
// path, or is the manifest's, so a module cannot have two names.
func CheckName(name string) error {
	if name == "" || name == ManifestName || strings.HasPrefix(name, "/") ||
		strings.Contains(name, `\`) || path.Clean(name) != name ||
		name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("%w: %q", ErrBadName, name)
	}
	return nil
}

// Write packs files as an archive whose entry module is entry, stamped with
// this binary's engine. A `.warp` member must have been compiled by this
// binary's engine, since that is the engine the manifest names.
//
// The output is deterministic: members are sorted and carry no timestamps, so
// one build produces one digest.
func Write(w io.Writer, entry string, files map[string][]byte) error {
	if _, ok := files[entry]; !ok {
		return fmt.Errorf("%w: %q", ErrNoEntry, entry)
	}
	if strings.HasSuffix(entry, CompiledExt) {
		return fmt.Errorf("%w: %q", ErrCompiledEntry, entry)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if err := CheckName(name); err != nil {
			return err
		}
		if source, ok := strings.CutSuffix(name, CompiledExt); ok {
			if _, has := files[source]; !has {
				return fmt.Errorf("%w: %q has no %q", ErrOrphanCompile, name, source)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)

	manifest, err := json.Marshal(Manifest{Format: Format, Entry: entry, Engine: Current()})
	if err != nil {
		return err
	}

	z := zip.NewWriter(w)
	if err := member(z, ManifestName, manifest); err != nil {
		return err
	}
	for _, name := range names {
		if err := member(z, name, files[name]); err != nil {
			return err
		}
	}
	return z.Close()
}

// member deflates every file: a `.warp` is about seven times its source and
// compresses well.
func member(z *zip.Writer, name string, data []byte) error {
	f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err == nil {
		_, err = f.Write(data)
	}
	return err
}
