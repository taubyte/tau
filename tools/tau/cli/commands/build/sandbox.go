package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// sandboxSource copies srcDir into a fresh temp directory and returns its path.
// Files matched by .gitignore (anywhere in the tree) are skipped — keeping the
// sandbox small and avoiding stale build caches (node_modules, target/, etc.)
// from leaking into a fresh build. The returned cleanup removes the temp dir;
// callers should defer it.
//
// Rationale: the local CLI shares the user's project directory with the build
// container as a writable bind mount. Build images routinely create caches,
// lockfiles, and symlinks under that directory, polluting the user's source
// tree. Copying into an isolated sandbox makes each build's filesystem effects
// predictable and disposable. Production (monkey) does not need this because
// it git-clones a fresh ephemeral workdir per job.
func sandboxSource(srcDir string) (sandbox string, cleanup func(), err error) {
	sandbox, err = os.MkdirTemp("", "tau-build-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating sandbox: %w", err)
	}
	cleanup = func() { os.RemoveAll(sandbox) }

	ignored, err := loadIgnoreMatcher(srcDir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("loading .gitignore in %s: %w", srcDir, err)
	}

	if err := copyTree(srcDir, sandbox, ignored); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copying %s to sandbox: %w", srcDir, err)
	}
	return sandbox, cleanup, nil
}

func loadIgnoreMatcher(srcDir string) (gitignore.Matcher, error) {
	patterns, err := gitignore.ReadPatterns(osfs.New(srcDir), nil)
	if err != nil {
		return nil, err
	}
	return gitignore.NewMatcher(patterns), nil
}

func copyTree(src, dst string, ignored gitignore.Matcher) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		parts := strings.Split(filepath.ToSlash(rel), "/")
		if ignored.Match(parts, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		target := filepath.Join(dst, rel)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
