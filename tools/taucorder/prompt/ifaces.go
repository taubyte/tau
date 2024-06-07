package prompt

import (
	goPrompt "github.com/c-bata/go-prompt"
	"github.com/taubyte/p2p/peer"
	auth "github.com/taubyte/tau/clients/p2p/auth"
	hoarder "github.com/taubyte/tau/clients/p2p/hoarder"
	monkey "github.com/taubyte/tau/clients/p2p/monkey"
	patrick "github.com/taubyte/tau/clients/p2p/patrick"
	seer "github.com/taubyte/tau/clients/p2p/seer"
	tnsIface "github.com/taubyte/tau/core/services/tns"
)

type Prompt interface {
	Run(peer.Node) error
	Done()
	Node() peer.Node
	TaubyteAuthClient() *auth.Client
	TaubyteSeerClient() *seer.Client
	TaubytePatrickClient() *patrick.Client
	TaubyteHoarderClient() *hoarder.Client
	TaubyteMonkeyClient() *monkey.Client
	TaubyteTnsClient() tnsIface.Client
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
