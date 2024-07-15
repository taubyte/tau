package states

import "context"

var Context context.Context
var ContextC context.CancelFunc

// Creating a basic context so that there is no nil contexts for tests or package usage
func init() {
	Context, ContextC = context.WithCancel(context.Background())
}

func New(ctx context.Context) {
	ContextC()
	Context, ContextC = context.WithCancel(ctx)
}
