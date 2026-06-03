package nextjs

import (
	"archive/zip"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// AssembleOptions configures building a Taubyte website asset from a Next build.
type AssembleOptions struct {
	// ProjectDir is the Next.js project root (contains `.next/`).
	ProjectDir string
	// Out is the website build zip to write.
	Out string
	// HandlerZip, when set, is the path to the server-bundle wasm archive (the
	// JS runtime that executes Next's edge handler). When empty the asset is
	// static-only: pre-rendered and static pages serve, dynamic routes do not.
	HandlerZip string
}

// Assemble builds a Taubyte website asset (the build zip) from a Next.js build:
// `.next/static` → `/_next/static`, `public/` → `/`, pre-rendered HTML into the
// asset, and — when a handler is supplied — the SSR manifest + server bundle.
func Assemble(opts AssembleOptions) (*Report, error) {
	manifest, report, err := Translate(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	out, err := os.Create(opts.Out)
	if err != nil {
		return nil, fmt.Errorf("creating output `%s` failed with: %w", opts.Out, err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)

	// Immutable hashed assets.
	if err := addDir(zw, filepath.Join(opts.ProjectDir, BuildDir, "static"), "_next/static"); err != nil {
		return nil, fmt.Errorf("adding _next/static failed with: %w", err)
	}
	// public/ is served from the root.
	if err := addDir(zw, filepath.Join(opts.ProjectDir, "public"), ""); err != nil {
		return nil, fmt.Errorf("adding public failed with: %w", err)
	}
	// Pre-rendered HTML (SSG/ISR) so the runtime's static check serves them.
	if err := addPrerendered(zw, opts.ProjectDir); err != nil {
		return nil, fmt.Errorf("adding pre-rendered html failed with: %w", err)
	}

	if opts.HandlerZip != "" {
		handler, err := os.ReadFile(opts.HandlerZip)
		if err != nil {
			return nil, fmt.Errorf("reading handler `%s` failed with: %w", opts.HandlerZip, err)
		}
		if err := addBytes(zw, websiteSpec.DefaultHandlerPath, handler); err != nil {
			return nil, err
		}
		mdata, err := manifest.Marshal()
		if err != nil {
			return nil, err
		}
		if err := addBytes(zw, websiteSpec.ManifestPath, mdata); err != nil {
			return nil, err
		}
		report.handlerEmbedded = true
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing asset failed with: %w", err)
	}

	return report, nil
}

// handlerEmbedded records whether a server bundle was included (for callers/logs).
func (r *Report) HandlerEmbedded() bool { return r.handlerEmbedded }

// addDir walks src and adds every regular file to the zip under destPrefix
// (slash separated, no leading slash). A missing src is a no-op.
func addDir(zw *zip.Writer, src, destPrefix string) error {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := path.Join(destPrefix, filepath.ToSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return addBytes(zw, name, data)
	})
}

// addPrerendered copies pre-rendered HTML emitted under `.next/server/{app,pages}`
// to its served path: `foo.html` -> `foo.html` (served for `/foo`),
// `index.html` -> `index.html` (served for `/`). Layout is version sensitive, so
// this is best-effort over any `.html` found there.
func addPrerendered(zw *zip.Writer, projectDir string) error {
	for _, sub := range []string{"server/app", "server/pages"} {
		root := filepath.Join(projectDir, BuildDir, sub)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return addBytes(zw, filepath.ToSlash(rel), data)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func addBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(strings.TrimPrefix(name, "/"))
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
