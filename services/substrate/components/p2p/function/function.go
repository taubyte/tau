package function

import (
	"fmt"
	"time"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func (f *Function) Commit() string {
	return f.commit
}

func (f *Function) Branch() string {
	return f.branch
}

func (f *Function) Project() string {
	return f.matcher.Project
}

func (f *Function) Close() {
	f.instanceCtxC()
}

func (f *Function) Handle(cmd *command.Command) (t time.Time, res response.Response, err error) {
	runtime, pluginApi, err := f.Instantiate()
	if err != nil {
		return t, nil, fmt.Errorf("instantiating function `%s` on project `%s` on application `%s` failed with: %s", f.config.Name, f.matcher.Project, f.matcher.Application, err)
	}
	defer runtime.Close()

	sdk, ok := pluginApi.(plugins.Instance)
	if !ok {
		return t, nil, fmt.Errorf("taubyte Plugin is not a plugin instance `%T`", pluginApi)
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

	return time.Now(), res, f.Call(runtime, ev.Id)
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

func (f *Function) AssetId() string {
	return f.assetId
}
