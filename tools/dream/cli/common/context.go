package common

import (
	"context"

	client "github.com/taubyte/tau/clients/http/dream"
)

type Context struct {
	Ctx        context.Context
	Multiverse *client.Client
}
