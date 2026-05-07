package common

import "github.com/taubyte/tau/utils/id"

var (
	// A wrap for the generate method for tests to override
	GetNewProjectID = id.Generate

	// Accounts ID generators — overridable in tests for deterministic IDs.
	GetNewAccountID = id.Generate
	GetNewMemberID  = id.Generate
	GetNewUserID    = id.Generate
	GetNewPlanID    = id.Generate
	GetNewSessionID = id.Generate
)
