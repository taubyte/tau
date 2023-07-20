package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/odo/protocols/auth/github"

	//fasthttp "github.com/valyala/fasthttp"
	http "github.com/taubyte/http"
	httpAuth "github.com/taubyte/http/auth"
)

/*
var githubAuthPrefix = []byte("github ")
var apikeyAuthPrefix = []byte("apikey ")

type GitHubAuthRequestHandler func(client *github.Client, ctx *fasthttp.RequestCtx)

type APIAuthContext map[string]interface{}

type APIContextVars map[string]interface{}

type APIRequestHandler func(actx *APIAuthContext, xvars *APIContextVars, ctx *fasthttp.RequestCtx) (map[string]interface{}, error)
*/

func (srv *AuthService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {
		rctx, rctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)
		client, err := github.New(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("invalid Github token")
		}
		ctx.SetVariable("GithubClient", client)
		ctx.SetVariable("GithubClientDone", rctx_cancel)
		logger.Debug(moodyCommon.Object{"message": fmt.Sprintf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())})
		return nil, nil
	}
	return nil, errors.New("valid Github token required")
}

func (srv *AuthService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	ctxVars := ctx.Variables()
	done, k := ctxVars["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}

/*
func (srv *AuthService) GitHubTokenAuth(f GitHubAuthRequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		auth := ctx.Request.Header.Peek("Authorization")
		if bytes.HasPrefix(auth, githubAuthPrefix) {
			token := string(auth[len(githubAuthPrefix):])

			rctx, ctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)
			defer ctx_cancel()

			client, err := github.New(rctx, token)
			if err != nil {
				ctx.SetStatusCode(fasthttp.StatusUnauthorized)
				return
			}

			f(client, ctx)
		} else {
			ctx.SetStatusCode(fasthttp.StatusUnauthorized)
		}
	}
}
*/
/*
func (srv *AuthService) APITokenAuth(f APIRequestHandler, contextVariables []string, scope []string) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {

		fmt.Println(ctx)
		fmt.Println("Headers: ", string(ctx.Request.Header.RawHeaders()))
		fmt.Println("Body: ", string(ctx.Request.Body()))
		fmt.Println("PostBody: ", string(ctx.PostBody()))

		auth := ctx.Request.Header.Peek("Authorization")

		xvars, err := srv.extractContextVars(ctx, contextVariables)
		if err != nil {
			fmt.Println("ERROR processing Vars: ", err)
			fmt.Fprint(ctx, err)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			return
		}

		if len(auth) > 0 {
			fmt.Println("Processing auth: ", string(auth))
			if bytes.HasPrefix(auth, apikeyAuthPrefix) {
				token := string(auth[len(apikeyAuthPrefix):])

				actx, err := srv.checkAPIToken(token, xvars)

				if err == nil {
					res, err := f(actx, xvars, ctx)
					srv.processResponse(ctx, res, err)
					return
				} // else -> out of IF

			} else if bytes.HasPrefix(auth, githubAuthPrefix) {
				token := string(auth[len(githubAuthPrefix):])

				fmt.Println("Processing github token: ", token)
				actx, err := srv.checkGithubAPIToken(token, xvars)
				fmt.Println("return: ", actx, " error:", err)

				if err == nil {
					res, err := f(actx, xvars, ctx)
					srv.processResponse(ctx, res, err)
					return
				}

			}
		}
		fmt.Println("No Auth!")
		ctx.SetStatusCode(fasthttp.StatusUnauthorized)
	}
}
*/
/*
func (srv *AuthService) checkAPIToken(token string, xvars *APIContextVars) (*APIAuthContext, error) {
	return nil, errors.New("Not Implemented")
}*/
/*
func (srv *AuthService) extractContextVars(ctx *fasthttp.RequestCtx, contextVariables []string) (*APIContextVars, error) {
	xvars := make(APIContextVars)

	var body map[string]interface{}
	_body := ctx.Request.Body()

	if len(_body) > 0 {
		err := json.Unmarshal(_body, &body)

		fmt.Println("BODY: ", string(_body))
		fmt.Println("JSON: ", body)
		fmt.Println("ERROR: ", err)
	}

	for _, k := range contextVariables {

		if v := ctx.UserValue(k); v != nil {
			xvars[k] = v.(string)
			continue
		} else if v := ctx.Request.Header.Peek(k); v != nil {
			xvars[k] = string(v)
			continue
		} else if v, ok := body[k]; ok {
			xvars[k] = v
			continue
		} else {
			return nil, errors.New("Key `" + k + "` not found!")
		}

	}

	fmt.Println("xvars: ", xvars)

	return &xvars, nil
}
*/
/*
func (srv *AuthService) checkGithubAPIToken(token string, xvars *APIContextVars) (*APIAuthContext, error) {

	project_id := toString((*xvars)["projectid"])

	if len(project_id) == 0 {
		return nil, errors.New("Project name invalid")
	}

	rctx, ctx_cancel := context.WithTimeout(srv.ctx, time.Duration(30)*time.Second)
	defer ctx_cancel()

	client, err := github.New(rctx, token)
	if err != nil {
		return nil, err
	}

	userLogin, err := srv.db.Get("/projects/" + project_id + "/owners/" + fmt.Sprintf("%d", *(client.Me().ID)))

	if err != nil {
		return nil, errors.New("Project does not exist or User not project owner.")
	}

	if string(userLogin) != client.Me().GetLogin() {
		return nil, errors.New("Github login mismatch")
	}

	actx := make(APIAuthContext)
	actx["token"] = token
	actx["scope"] = []string{"all"}

	return &actx, nil

}
*/
