package function

import (
	"errors"
	"fmt"
	"time"

	goHttp "net/http"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func (f *Function) Project() string {
	return f.project
}

func (f *Function) Handle(w goHttp.ResponseWriter, r *goHttp.Request, matcher commonIface.MatchDefinition) (t time.Time, err error) {
	runtime, pluginApi, err := f.module.Instantiate()
	if err != nil {
		return t, fmt.Errorf("instantiating function `%s` on project `%s` on application `%s` failed with: %w", f.config.Name, f.project, f.application, err)
	}
	defer runtime.Close()

	sdk, ok := pluginApi.(plugins.Instance)
	if !ok {
		return t, fmt.Errorf("taubyte Plugin is not a plugin instance `%T`", pluginApi)
	}

	ev := sdk.CreateHttpEvent(w, r)

	val, err := f.SmartOps()
	if err != nil || val > 0 {
		if err != nil {
			return t, fmt.Errorf("running smart ops failed with: %s", err)
		}
		return t, fmt.Errorf("exited: %d", val)
	}

	return time.Now(), f.module.Call(runtime, ev.Id)
}

func (f *Function) Match(matcher commonIface.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	if _matcher.Method == f.config.Method {
		for _, path := range f.config.Paths {
			if path == _matcher.Path {
				currentMatch = matcherSpec.HighMatch
			}
		}
	}

	return currentMatch
}

func (f *Function) Validate(matcher commonIface.MatchDefinition) error {
	if f.Match(matcher) == matcherSpec.NoMatch {
		return fmt.Errorf("function method or paths do not match: %v != %v || %v != %v", f.config.Method, f.matcher.Method, f.matcher.Path, f.config.Paths)
	}

	if len(f.config.Domains) == 0 {
		return errors.New("non-http function")
	}

	return nil
}

// TODO the below are generic
func (f *Function) Service() commonIface.ServiceComponent {
	return f.srv
}

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

func (f *Function) Commit() string {
	return f.commit
}

func (f *Function) Matcher() commonIface.MatchDefinition {
	return f.matcher
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

func (f *Function) CachePrefix() string {
	return f.matcher.Host
}

func (f *Function) Application() string {
	return f.application
}
