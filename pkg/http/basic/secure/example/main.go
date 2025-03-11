package main

import (
	"context"
	"log"

	http "github.com/taubyte/tau/pkg/http"
	auth "github.com/taubyte/tau/pkg/http/auth"
	https "github.com/taubyte/tau/pkg/http/basic/secure"
	"github.com/taubyte/tau/pkg/http/options"
)

func main() {
	srv, err := https.New(context.Background(), options.Listen(":11111"), options.AllowedMethods([]string{"GET"}), options.SelfSignedCertificate())
	if err != nil {
		log.Fatalf("basicHttp New on 11111 failed with: %s", err)
	}

	srv.GET(&http.RouteDefinition{
		Path: "/ping/{who}",
		Vars: http.Variables{
			Required: []string{"who"},
		},
		Auth: http.RouteAuthHandler{
			Validator: auth.AnonymousHandler,
		},
		Handler: func(c http.Context) (interface{}, error) {
			who, _ := c.GetStringVariable("who")
			return map[string]string{"ping": who}, nil
		},
	})

	srv.Start()

	err = srv.Wait()
	if err != nil {
		log.Fatalf("secure example stopped with error: %s", err)
	}
}
