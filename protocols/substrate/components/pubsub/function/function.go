package function

import (
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/go-interfaces/services/substrate/components"

	matcherSpec "github.com/taubyte/go-specs/matcher"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func (f *Function) Commit() string {
	return f.commit
}

func (f *Function) Project() (cid.Cid, error) {
	return cid.Decode(f.matcher.Project)
}

func (f *Function) HandleMessage(msg *pubsub.Message) (t time.Time, err error) {
	instance, runtime, plugin, err := f.function.Instantiate(components.FunctionContext{
		Config:      f.config,
		Project:     f.matcher.Project,
		Application: f.matcher.Application,
	}, f.srv.Branch(), f.commit)
	if err != nil {
		return t, fmt.Errorf("instantiating function `%s` on project `%s` on application `%s` failed with: %s", f.config.Name, f.matcher.Project, f.matcher.Application, err)
	}
	defer runtime.Close()

	sdk, ok := plugin.(plugins.Instance)
	if !ok {
		return t, fmt.Errorf("taubyte Plugin is not a plugin instance `%T`", plugin)
	}

	ev := sdk.CreatePubsubEvent(msg)
	val, err := f.SmartOps(ev)
	if err != nil {
		return t, fmt.Errorf("running smart ops failed with: %s", err)
	}
	if val > 0 {
		return t, fmt.Errorf("exited: %d", val)
	}

	return time.Now(), instance.Call(runtime, ev.Id)
}

func (f *Function) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	if len(f.mmi.Matches(_matcher.Channel)) > 0 {
		currentMatch = matcherSpec.HighMatch
	}

	return currentMatch
}

func (f *Function) Matcher() components.MatchDefinition {
	return f.matcher
}

func (f *Function) Validate(matcher components.MatchDefinition) error {
	if f.Match(f.matcher) == matcherSpec.NoMatch {
		return errors.New("function channels do not match")
	}

	return nil
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

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

func (f *Function) MMI() common.MessagingMapItem {
	return f.mmi
}

func (f *Function) Service() components.ServiceComponent {
	return f.srv
}
