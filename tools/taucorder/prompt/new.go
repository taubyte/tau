package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	auth "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/p2p/peer"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/google/shlex"
	dreamland "github.com/taubyte/tau/clients/http/dream"
	hoarder "github.com/taubyte/tau/clients/p2p/hoarder"
	monkey "github.com/taubyte/tau/clients/p2p/monkey"
	patrick "github.com/taubyte/tau/clients/p2p/patrick"
	seer "github.com/taubyte/tau/clients/p2p/seer"
	tns "github.com/taubyte/tau/clients/p2p/tns"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/tools/taucorder/common"

	authIface "github.com/taubyte/tau/core/services/auth"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	monkeyIface "github.com/taubyte/tau/core/services/monkey"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	seerIface "github.com/taubyte/tau/core/services/seer"
)

type tcprompt struct {
	ctx             context.Context
	ctxC            context.CancelFunc
	engine          *goPrompt.Prompt
	path            string
	node            peer.Node
	scanner         ScannerHandler
	authClient      authIface.Client
	seerClient      seerIface.Client
	hoarderClient   hoarderIface.Client
	monkeyClient    monkeyIface.Client
	tnsClient       tnsIface.Client
	patrickClient   patrickIface.Client
	dreamlandClient *dreamland.Client
}

var prompt Prompt

func New(ctx context.Context) (Prompt, error) {
	if prompt != nil {
		return prompt, nil
	}

	p := &tcprompt{
		path: "/",
	}

	prompt = p

	p.ctx, p.ctxC = common.GlobalContext, common.GlobalContextCancel

	p.engine = goPrompt.New(
		func(s string) {
			args, err := shlex.Split(s)
			if err != nil {
				args = strings.Split(strings.TrimSpace(s), " ")
			}
			forest.Execute(p, args)
		},
		func(in goPrompt.Document) []goPrompt.Suggest {
			ret := forest.Complete(prompt, in)
			return ret
		},

		goPrompt.OptionLivePrefix(func() (prefix string, useLivePrefix bool) {
			return prompt.CurrentPath() + "> ", true
		}),
		goPrompt.OptionTitle("taucorder"),
		goPrompt.OptionCompletionOnDown(),
	)

	return prompt, nil
}

func (p *tcprompt) Run(options ...Option) error {

	for _, opt := range options {
		if err := opt(p); err != nil {
			return err
		}
	}

	if p.node == nil {
		return errors.New("you need to select a cloud")
	}

	err := p.node.WaitForSwarm(10 * time.Second)
	if err != nil {
		return err
	}

	p.authClient, err = auth.New(p.ctx, p.node)
	if err != nil {
		return err
	}

	p.seerClient, err = seer.New(p.ctx, p.node)
	if err != nil {
		return err
	}

	pc, err := patrick.New(p.ctx, p.node)
	if err != nil {
		return err
	}
	p.patrickClient = pc.(*patrick.Client)

	p.hoarderClient, err = hoarder.New(p.ctx, p.node)
	if err != nil {
		return err
	}

	p.monkeyClient, err = monkey.New(p.ctx, p.node)
	if err != nil {
		return err
	}

	p.tnsClient, err = tns.New(p.ctx, p.node)
	if err != nil {
		return err
	}

	p.dreamlandClient, err = dreamland.New(p.ctx)
	if err != nil {
		return err
	}

	p.engine.Run()

	fmt.Println()

	return nil
}

func (p *tcprompt) Done() {
	p.ctxC()
}

func (p *tcprompt) Context() context.Context {
	return p.ctx
}

func (p *tcprompt) Node() peer.Node {
	return p.node
}

func (p *tcprompt) CurrentPath() string {
	return p.path
}

func (p *tcprompt) SetPath(path string) {
	p.path = path
}

func (p *tcprompt) AuthClient() authIface.Client {
	return p.authClient
}

func (p *tcprompt) SeerClient() seerIface.Client {
	return p.seerClient
}

func (p *tcprompt) PatrickClient() patrickIface.Client {
	return p.patrickClient
}

func (p *tcprompt) HoarderClient() hoarderIface.Client {
	return p.hoarderClient
}

func (p *tcprompt) MonkeyClient() monkeyIface.Client {
	return p.monkeyClient
}

func (p *tcprompt) TnsClient() tnsIface.Client {
	return p.tnsClient
}
