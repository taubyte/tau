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

	b := &assetZip{zw: zip.NewWriter(out), seen: map[string]bool{}}

	// Immutable hashed assets.
	if err := b.addDir(filepath.Join(opts.ProjectDir, BuildDir, "static"), "_next/static"); err != nil {
		return nil, fmt.Errorf("adding _next/static failed with: %w", err)
	}
	// public/ is served from the root.
	if err := b.addDir(filepath.Join(opts.ProjectDir, "public"), ""); err != nil {
		return nil, fmt.Errorf("adding public failed with: %w", err)
	}
	// Pre-rendered HTML (SSG/ISR) so the runtime's static check serves them.
	if err := b.addPrerendered(opts.ProjectDir); err != nil {
		return nil, fmt.Errorf("adding pre-rendered html failed with: %w", err)
	}

	if opts.HandlerZip != "" {
		handler, err := os.ReadFile(opts.HandlerZip)
		if err != nil {
			return nil, fmt.Errorf("reading handler `%s` failed with: %w", opts.HandlerZip, err)
		}
		if err := b.addFile(websiteSpec.DefaultHandlerPath, handler); err != nil {
			return nil, err
		}
		mdata, err := manifest.Marshal()
		if err != nil {
			return nil, err
		}
		if err := b.addFile(websiteSpec.ManifestPath, mdata); err != nil {
			return nil, err
		}
		report.handlerEmbedded = true
	}

	if err := b.zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing asset failed with: %w", err)
	}

	return report, nil
}

// HandlerEmbedded reports whether a server bundle was included (for callers/logs).
func (r *Report) HandlerEmbedded() bool { return r.handlerEmbedded }

// assetZip writes de-duplicated, slash-separated entries to a website asset zip.
type assetZip struct {
	zw   *zip.Writer
	seen map[string]bool
}

func (b *assetZip) addFile(name string, data []byte) error {
	name = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(name)), "/")
	if name == "" || b.seen[name] {
		return nil
	}
	b.seen[name] = true
	w, err := b.zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// addDir walks src and adds every regular file under destPrefix. Missing src is
// a no-op.
func (b *assetZip) addDir(src, destPrefix string) error {
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
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
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return b.addFile(path.Join(destPrefix, filepath.ToSlash(rel)), data)
	})
}

// addPrerendered copies pre-rendered HTML emitted under `.next/server/{app,pages}`.
// Because the static handler resolves a request by exact path (falling back to a
// directory's index.html, never `<path>.html`), each `foo.html` is also written
// as `foo/index.html` so a clean URL `/foo` resolves. `index.html` is kept as is.
func (b *assetZip) addPrerendered(projectDir string) error {
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
			name := filepath.ToSlash(rel)
			if err := b.addFile(name, data); err != nil {
				return err
			}
			// clean-URL form: about.html -> about/index.html (so /about resolves)
			if path.Base(name) != "index.html" {
				clean := strings.TrimSuffix(name, ".html") + "/index.html"
				if err := b.addFile(clean, data); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
