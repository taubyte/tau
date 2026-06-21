// Command taubyte-next-adapter turns a Next.js production build (`.next/`) into a
// Taubyte website asset: it maps Next's routing/manifests onto Taubyte's SSR
// manifest, publishes static + pre-rendered output, and (when given a server
// bundle) wires in the SSR handler.
//
// The server bundle — the JS runtime that executes Next's edge handler — is a
// pluggable input (--handler). Without it the asset is static-only: pre-rendered
// and static pages serve, dynamic/SSR/API routes need the bundle. Producing that
// bundle (Web-API + Node-compat layer over Javy) is the runtime phase; see
// docs/nextjs-adapter.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/taubyte/tau/pkg/specs/builders/frameworks/nextjs"
)

func main() {
	project := flag.String("project", ".", "Next.js project root (contains .next/)")
	out := flag.String("out", "build.zip", "output website build zip")
	handler := flag.String("handler", "", "optional server-bundle wasm zip; enables SSR/api")
	flag.Parse()

	rep, err := nextjs.Assemble(nextjs.AssembleOptions{
		ProjectDir: *project,
		Out:        *out,
		HandlerZip: *handler,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "taubyte-next-adapter:", err)
		os.Exit(1)
	}

	fmt.Printf("taubyte-next-adapter: wrote %s\n", *out)
	fmt.Printf("  basePath:    %q\n", rep.BasePath)
	fmt.Printf("  prerendered: %d\n", len(rep.PrerenderedRoutes))
	fmt.Printf("  dynamic:     %d\n", len(rep.DynamicRoutes))
	fmt.Printf("  api routes:  %d\n", len(rep.APIRoutes))
	fmt.Printf("  middleware:  %v\n", rep.HasMiddleware)
	if !rep.HandlerEmbedded() {
		fmt.Println("  NOTE: no --handler given — asset is static-only (pre-rendered + static pages).")
		fmt.Println("        Dynamic/SSR/API routes require the server bundle (runtime phase).")
	}
}
