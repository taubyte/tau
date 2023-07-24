package auth

//fasthttp "github.com/valyala/fasthttp"

/*
func (srv *AuthService) processResponse(ctx *fasthttp.RequestCtx, response map[string]interface{}, err error) {
	fmt.Println("processResponse: ", response)
	fmt.Println("processResponse: ERROR=", err)

	message, err2 := json.Marshal(response)
	fmt.Fprint(ctx, string(message))

	if err != nil && err2 != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
	} else {
		ctx.SetStatusCode(fasthttp.StatusOK)
	}
}*/

func (srv *AuthService) setupHTTPRoutes() {
	srv.setupGitHubHTTPRoutes()
	srv.setupDevicesHTTPRoutes()
	srv.setupDomainsHTTPRoutes()
}

/*func (srv *AuthService) serveHTTP(laddr string) {

	go func() {
		withCors := cors.NewCorsHandler(cors.Options{
			// if you leave allowedOrigins empty then fasthttpcors will treat it as "*"
			//	AllowedOrigins: []string{"http://example.com"}, // Only allow example.com to access the resource
			// if you leave allowedHeaders empty then fasthttpcors will accept any non-simple headers
			//	AllowedHeaders: []string{"x-something-client", "Content-Type"}, // only allow x-something-client and Content-Type in actual request
			// if you leave this empty, only simple method will be accepted
			AllowedMethods: []string{"GET", "PUT", "POST", "DELETE"}, // only allow get or post to resource
			//AllowCredentials: false,                   // resource doesn't support credentials
			//AllowMaxAge:      5600,                    // cache the preflight result
			Debug: true,
		})
		fasthttp.ListenAndServeTLS(laddr, "/tb/priv/fullchain.pem", "/tb/priv/privkey.pem", withCors.CorsMiddleware(srv.router.Handler))
	}()
}*/
