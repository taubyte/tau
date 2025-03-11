package main

import (
	"archive/zip"
	"context"
	"log"
	"os"

	"github.com/spf13/afero/zipfs"
	http "github.com/taubyte/tau/pkg/http"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"

	_ "embed"
)

func main() {
	filename := "./files/build.zip"

	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("opening `%s` failed with: %s", filename, err)
	}

	fStat, err := f.Stat()
	if err != nil {
		log.Fatalf("stat of `%s` failed with: %s", filename, err)
	}

	zipReader, err := zip.NewReader(f, fStat.Size())
	if err != nil {
		log.Fatalf("zip NewReader of `%s` failed with: %s", filename, err)
	}

	srv, err := basicHttp.New(context.Background(), options.Listen(":8089"))
	if err != nil {
		log.Fatalf("basicHttp New failed with: %s", err)
	}

	asset := &http.HeadlessAssetsDefinition{
		FileSystem:            zipfs.New(zipReader),
		SinglePageApplication: true,
	}
	srv.Raw(&http.RawRouteDefinition{
		PathPrefix: "/",
		Handler: func(ctx http.Context) (interface{}, error) {
			return srv.AssetHandler(asset, ctx)
		},
	})

	srv.Start()
	err = srv.Wait()
	if err != nil {
		log.Fatalf("basic example stopped with error: %s", err)
	}
}
