// Package protogen implements the buf-output drift check: regenerate a buf
// module with the pinned toolchain into a scratch directory and diff the
// result, byte for byte, against what is already committed under its gen/
// directory. It is the buf equivalent of tools/tcc-gen's TestNoDrift — the
// same shape, for the other generator this repo runs by hand.
//
// A byte diff alone cannot see every version drift: protoc-gen-go stamps
// its own version into what it generates, so a stale copy shows up as a
// diff on its own, but protoc-gen-connect-go stamps nothing — see
// pluginPins in plugins.go. checkPluginVersions asks each pinned plugin
// directly, via --version, rather than relying on that accident.
package protogen

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Diff describes one file that does not match between the freshly generated
// output and the tree already committed at protoDir/gen.
type Diff struct {
	Rel  string // path relative to gen/, forward slashes
	Kind string // "generated", "committed", or "differs" — see PrintReport
}

// Result is the outcome of Check.
type Result struct {
	Dir     string // the module directory that was checked, relative to repo root
	Checked int    // total distinct files considered on either side of the diff
	Diffs   []Diff
}

// Check regenerates the buf module at repoRoot/protoDir into a scratch
// directory — `buf generate -o <scratch>`, run with that directory as the
// working directory so it picks up the module's own buf.gen.yaml exactly as
// a developer running `buf generate` there would; -o prepends to the out:
// paths in the template rather than replacing them, so the result lands at
// <scratch>/gen — and diffs it against the tree already committed at
// protoDir/gen.
//
// Checked is always the union of both sides, not just what regeneration
// produced: if the generator silently starts producing nothing, whatever is
// still committed keeps the count non-zero and every one of those files
// shows up as a diff, rather than a vacuous 0-checked, 0-diff pass. That
// silent shrink is a real failure mode in this repo — commit 481fab44 took
// tools/tcc-gen's own check from 57 files to 2 without going red.
//
// A module with no .proto sources yet is not run through buf at all (buf
// generate hard-errors on an empty module); Check instead requires gen/ to
// be equally empty, which still catches a stale gen/ tree left behind after
// every .proto in a module was removed. For the same reason, checkPluginVersions
// only runs once there is source to generate from — there is nothing to pin
// a toolchain version against otherwise.
func Check(repoRoot, protoDir string) (Result, error) {
	absDir := filepath.Join(repoRoot, protoDir)

	hasSource, err := anyProtoFiles(absDir)
	if err != nil {
		return Result{}, fmt.Errorf("scan %s for .proto sources: %w", protoDir, err)
	}

	generated := map[string][]byte{}
	if hasSource {
		if _, err := lookBuf(); err != nil {
			return Result{}, fmt.Errorf("buf not found on PATH (required to check %s): %w", protoDir, err)
		}
		if err := checkPluginVersions(repoRoot, absDir); err != nil {
			return Result{}, fmt.Errorf("plugin version check for %s: %w", protoDir, err)
		}

		scratch, err := os.MkdirTemp("", "protogen-check-")
		if err != nil {
			return Result{}, err
		}
		defer os.RemoveAll(scratch)

		if err := generate(absDir, scratch); err != nil {
			return Result{}, fmt.Errorf("buf generate in %s: %w", protoDir, err)
		}

		generated, err = readTree(filepath.Join(scratch, "gen"))
		if err != nil {
			return Result{}, err
		}
	}

	committed, err := readTree(filepath.Join(absDir, "gen"))
	if err != nil {
		return Result{}, err
	}

	rels := make(map[string]bool, len(generated)+len(committed))
	for rel := range generated {
		rels[rel] = true
	}
	for rel := range committed {
		rels[rel] = true
	}

	var diffs []Diff
	for rel := range rels {
		g, gOK := generated[rel]
		c, cOK := committed[rel]
		switch {
		case !cOK:
			diffs = append(diffs, Diff{Rel: rel, Kind: "generated"})
		case !gOK:
			diffs = append(diffs, Diff{Rel: rel, Kind: "committed"})
		case !bytes.Equal(g, c):
			diffs = append(diffs, Diff{Rel: rel, Kind: "differs"})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Rel < diffs[j].Rel })

	return Result{Dir: protoDir, Checked: len(rels), Diffs: diffs}, nil
}

// lookBuf resolves the buf binary on PATH, isolated in its own function so a
// missing-toolchain error path is easy to test independently of buf actually
// being installed.
func lookBuf() (string, error) {
	return exec.LookPath("buf")
}

// generate runs `buf generate -o out` with dir as the working directory, so
// it picks up dir's own buf.gen.yaml exactly as a developer running `buf
// generate` there would. -o prepends to the template's out: paths rather
// than replacing them, so a template with `out: gen` lands at out/gen.
func generate(dir, out string) error {
	cmd := exec.Command("buf", "generate", "-o", out)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n%s", err, stderr.String())
	}
	return nil
}

// PrintReport writes a human-readable report to w, always ending with the
// "N files checked, M with differences" line — the count that keeps this
// check from silently passing over nothing.
func PrintReport(w io.Writer, r Result) {
	for _, d := range r.Diffs {
		switch d.Kind {
		case "generated":
			fmt.Fprintf(w, "DIFF  %s: %s\n        + only in regenerated output (missing from the committed tree)\n", r.Dir, d.Rel)
		case "committed":
			fmt.Fprintf(w, "DIFF  %s: %s\n        - only in the committed tree (orphaned; no longer generated)\n", r.Dir, d.Rel)
		case "differs":
			fmt.Fprintf(w, "DIFF  %s: %s\n        ! regenerated output differs (run `make proto-v2` and commit it)\n", r.Dir, d.Rel)
		}
	}
	fmt.Fprintf(w, "\n%s: %d generated files checked, %d with differences\n", r.Dir, r.Checked, len(r.Diffs))
}

// FindRepoRoot walks up from dir to the tau module root, the same way
// tools/tcc-gen locates it.
func FindRepoRoot(dir string) (string, error) {
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(b), "module github.com/taubyte/tau") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate tau module root (go.mod) above %s", dir)
		}
		dir = parent
	}
}

// anyProtoFiles reports whether dir (excluding its gen/ subdirectory)
// contains any .proto source — the same test buf itself makes internally, a
// module with none is a `buf generate` hard error, not a vacuous success.
func anyProtoFiles(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return nil // module directory doesn't exist yet: nothing to find
			}
			return err
		}
		if d.IsDir() && d.Name() == "gen" && path != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".proto") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// readTree reads every regular file under root into memory, keyed by its
// path relative to root with forward slashes. A missing root is an empty
// tree, not an error — there may be nothing generated, or nothing committed,
// yet.
func readTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
