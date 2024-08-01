package vm

import "context"

type Context interface {
	// Context returns the go context of the function instance
	Context() context.Context

	// Project returns the Taubyte project id
	Project() string

	// Application returns the application, if none returns an empty string
	Application() string

	// Resource returns the id of the resource being used.
	Resource() string

	// Branches returns the branch name used by this resource execution pipeline.
	Branches() []string

	// Commit returns the commit id used by this resource execution pipeline.
	Commit() string

	// Clone clones the VM.Context with a new go context
	Clone(context.Context) Context
}
