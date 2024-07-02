package prompt

import (
	"context"

	goPrompt "github.com/c-bata/go-prompt"
	auth "github.com/taubyte/tau/core/services/auth"
	hoarder "github.com/taubyte/tau/core/services/hoarder"
	monkey "github.com/taubyte/tau/core/services/monkey"

	patrick "github.com/taubyte/tau/core/services/patrick"

	seer "github.com/taubyte/tau/core/services/seer"

	tns "github.com/taubyte/tau/core/services/tns"

	"github.com/taubyte/tau/p2p/peer"
)

type Option func(Prompt) error

type Prompt interface {
	Run(...Option) error
	Done()
	Context() context.Context
	Node() peer.Node
	AuthClient() auth.Client
	SeerClient() seer.Client
	PatrickClient() patrick.Client
	HoarderClient() hoarder.Client
	MonkeyClient() monkey.Client
	TnsClient() tns.Client
	CurrentPath() string
	SetPath(string)
}

type Tree interface {
	Complete(Prompt, goPrompt.Document) []goPrompt.Suggest
	Execute(p Prompt, args []string) error
}

type Forest interface {
	Tree
	FindBranch(path string) ([]*leaf, error)
}
