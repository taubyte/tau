package vm

import api "github.com/samyfodil/wazy/api"

// Module is a wazy-instantiated module, exposed directly. wazy is the only
// engine, so there is no abstraction to wrap it (the old callBridge is gone).
type Module = api.Module
