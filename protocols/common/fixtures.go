package common

import (
	idutils "github.com/taubyte/utils/id"
)

var (
	// A wrap for the generate method for tests to override
	GetNewProjectID = idutils.Generate
)
