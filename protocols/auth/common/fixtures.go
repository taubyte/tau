package common

import (
	idutils "github.com/taubyte/utils/id"
)

// A wrap for the generate method for tests to override
var GetNewProjectID = idutils.Generate
