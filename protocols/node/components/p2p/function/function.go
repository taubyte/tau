package function

import (
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func (f *Function) Commit() string {
	return f.commit
}

func (f *Function) Project() (cid.Cid, error) {
	return cid.Decode(f.matcher.Project)
}

func (f *Function) Close() {
	f.instanceCtxC()
}

func (f *Function) Handle(cmd *command.Command) (t time.Time, res response.Response, err error) {
	instance, runtime, plugin, err := f.function.Instantiate(commonIface.FunctionContext{
		Config:      f.config,
		Project:     f.matcher.Project,
		Application: f.matcher.Application,
	}, f.srv.Branch(), f.commit)
	if err != nil {
		return t, nil, fmt.Errorf("instantiating function `%s` on project `%s` on application `%s` failed with: %s", f.config.Name, f.matcher.Project, f.matcher.Application, err)
	}
	defer runtime.Close()

	sdk, ok := plugin.(plugins.Instance)
	if !ok {
		return t, nil, fmt.Errorf("taubyte Plugin is not a plugin instance `%T`", plugin)
	}

	res = make(response.Response)
	ev := sdk.CreateP2PEvent(cmd, res)
	if len(f.config.SmartOps) > 0 {
		val, err := f.SmartOps()
		if err != nil || val > 0 {
			if err != nil {
				return t, nil, fmt.Errorf("running smart ops failed with: %s", err)
			}

			res.Set("code", val)
			return t, res, fmt.Errorf("exited: %d", val)
		}
	}

	return time.Now(), res, instance.Call(runtime, ev.Id)
}

func (f *Function) Match(matcher commonIface.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*iface.MatchDefinition)
	if !ok {
		return
	}

	if _matcher.Command == f.config.Command && _matcher.Protocol == f.config.Protocol {
		currentMatch = matcherSpec.HighMatch
	}

	return currentMatch
}

func (f *Function) Validate(matcher commonIface.MatchDefinition) error {
	if f.Match(matcher) != matcherSpec.HighMatch {
		err := fmt.Sprintf("%s != %s || %s != %s", f.config.Command, f.matcher.Command, f.config.Protocol, f.matcher.Protocol)
		return fmt.Errorf("function commands || services do not match: %s", err)
	}

	return nil
}

func (f *Function) Matcher() commonIface.MatchDefinition {
	return f.matcher
}

func (f *Function) Name() string {
	return f.config.Name
}

func (f *Function) Id() string {
	return f.config.Id
}

func (f *Function) Ready() error {
	if !f.readyDone {
		<-f.readyCtx.Done()
	}

	return f.readyError
}

func (f *Function) Application() string {
	return f.matcher.Application
}

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

func (f *Function) Service() commonIface.ServiceComponent {
	return f.srv
}
