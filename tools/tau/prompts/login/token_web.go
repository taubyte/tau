package loginPrompts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/pterm/pterm"
	http "github.com/taubyte/tau/pkg/http"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
	"github.com/urfave/cli/v2"
)

type SessionData struct {
	Expiry       float64  `json:"exp"`
	Provider     string   `json:"provider"`
	Repositories []string `json:"repositories"`
	Token        string   `json:"token"`
}

func extractTokenFromSession(session string) (data SessionData, err error) {
	sessionSplit := strings.Split(session, ".")
	if len(sessionSplit) < 1 {
		err = fmt.Errorf("invalid session: `%s`", session)
		return
	}

	base64Decoded, err := base64.RawStdEncoding.DecodeString(sessionSplit[1])
	if err != nil {
		err = fmt.Errorf("decoding session `%s` failed with: %s", session, err)
		return
	}

	err = json.Unmarshal(base64Decoded, &data)
	if err != nil {
		return
	}

	return
}

func getTokenConsoleURL(provider string, origin string) string {
	consoleURL := "console.taubyte.com/"
	consoleURL = "https://" + path.Join(consoleURL, fmt.Sprintf("oauth/%s/login", provider))
	return consoleURL + fmt.Sprintf("?origin=%s", origin)
}

// Token from web gives a link to github with a hook back to here
func TokenFromWeb(ctx *cli.Context, provider string) (token string, err error) {
	tokenCh := make(chan string)
	defer close(tokenCh)

	errCh := make(chan error)
	defer close(errCh)

	// Open an http server to listen for the token
	srv, err := basicHttp.New(ctx.Context, options.Listen(":"+githubLoginListenPort))
	if err != nil {
		err = fmt.Errorf(StartingHttpFailedWith, githubLoginListenPort, err)
		return
	}

	srv.GET(&http.RouteDefinition{
		Path: "/",
		Handler: func(ctx http.Context) (iface interface{}, err error) {
			session := ctx.Request().URL.Query().Get("session")
			if len(session) == 0 {
				errCh <- errors.New(NoSessionProvided)
				return
			}

			sessionData, err := extractTokenFromSession(session)
			if err != nil {
				errCh <- err
				return
			}

			// TODO track expiration of token
			tokenCh <- sessionData.Token

			return SuccessCheckBackAtYourTerminal, nil
		},
	})

	// Start the http server listen
	srv.Start()

	origin := fmt.Sprintf("http://127.0.0.1:%s", githubLoginListenPort)
	pterm.Info.Printfln(OpenURLToLogin, provider, getTokenConsoleURL(provider, origin))

	select {
	case token = <-tokenCh:
		break
	case err = <-errCh:
		break
	}

	// Stop the http server
	srv.Stop()
	if srv.Error() != nil {
		// Only display error, as we got the token or an error
		pterm.Warning.Printfln(ShuttingDownHttpFailedWith, githubLoginListenPort, srv.Error())
	}

	return
}
