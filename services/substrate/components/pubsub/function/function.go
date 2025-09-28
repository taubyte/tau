package function

import (
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"

	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
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

func (f *Function) HandleMessage(msg iface.Message) (t time.Time, err error) {
	instance, err := f.Instantiate(f.instanceCtx)
	if err != nil {
		return t, fmt.Errorf("instantiating function `%s` on project `%s` on application `%s` failed with: %s", f.config.Name, f.matcher.Project, f.matcher.Application, err)
	}
	defer instance.Free()

	// Pass the interface message directly to the SDK
	ev := instance.SDK().CreatePubsubEvent(msg)

	return time.Now(), f.Call(instance, ev.Id)
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

func (f *Function) AssetId() string {
	return f.assetId
}
