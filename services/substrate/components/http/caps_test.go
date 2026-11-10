package http

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/runtime"
)

type capped struct{ substrate.Service }

func (capped) Caps(string) config.Caps { return config.Caps{FunctionMemory: 1} }

type cappedFunction struct {
	components.FunctionServiceable
	service components.ServiceComponent
}

func (f *cappedFunction) Config() *structureSpec.Function {
	return &structureSpec.Function{Name: "f", Memory: 2}
}
func (f *cappedFunction) Service() components.ServiceComponent { return f.service }
func (f *cappedFunction) Project() string                      { return "p" }
func (f *cappedFunction) Branch() string                       { return "main" }
func (f *cappedFunction) Commit() string                       { return "c" }

// A real function reaches the node's caps through the real http component,
// which only embeds substrate.Service: the path that once saw no caps at all.
func TestFunctionSeesCapsThroughComponent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := runtime.New(ctx, &cappedFunction{service: &Service{Service: capped{}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Instantiate(ctx); platformerr.CodeOf(err) != platformerr.OverQuota {
		t.Fatalf("got %v (%q), want over_quota", err, platformerr.CodeOf(err))
	}
}
