package common

import "github.com/taubyte/utils/id"

var (
	// A wrap for the generate method for tests to override
	GetNewProjectID = id.Generate
)
