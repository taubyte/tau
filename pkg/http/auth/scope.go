package auth

import (
	"bytes"

	service "github.com/taubyte/tau/pkg/http"
)

func AnonymousHandler(ctx service.Context) (interface{}, error) {
	return nil, nil
}

func Scope(scope []string, authHandler service.Handler) service.Handler {
	return func(ctx service.Context) (interface{}, error) {
		auth := []byte(ctx.Request().Header.Get("Authorization"))

		len_auth := len(auth)
		if len_auth > 0 {
			for _, tkn := range AllowedTokenTypes {
				if len_auth > tkn.length+1 && bytes.HasPrefix(auth, tkn.value) {
					ctx.Variables()["Authorization"] = Authorization{
						Type:  tkn.name,
						Token: string(auth[tkn.length+1:]),
						Scope: scope,
					}
				}
			}
		}

		return nil, ctx.HandleAuth(authHandler)
	}
}

func GetAuthorization(c service.Context) *(Authorization) {
	if a, ok := c.Variables()["Authorization"]; ok {
		if v, ok := a.(Authorization); ok {
			return &v
		}
	}

	return nil
}
