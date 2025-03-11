package main

import (
	"context"
	"log"

	http "github.com/taubyte/tau/pkg/http"
	auth "github.com/taubyte/tau/pkg/http/auth"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
)

func main() {
	srv, err := basicHttp.New(context.Background(), options.Listen(":11111"), options.AllowedMethods([]string{"GET"}), options.AllowedOrigins(false, []string{"*"}))
	if err != nil {
		log.Fatalf("start basic http on 11111 failed with: %s", err)
	}

	srv.GET(&http.RouteDefinition{
		Path: "/ping",
		Auth: http.RouteAuthHandler{
			Validator: auth.AnonymousHandler,
		},
		Handler: func(http.Context) (interface{}, error) { return map[string]string{"ping": "pong"}, nil },
	})

	srv.Start()

	err = srv.Wait()
	if err != nil {
		log.Fatalf("basic example stopped with error: %s", err)
	}
}
