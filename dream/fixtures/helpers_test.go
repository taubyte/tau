package fixtures

import (
	commonTest "github.com/taubyte/tau/dream/helpers"
)

var (
	fakeMeta = commonTest.ConfigRepo.HookInfo
)

func init() {
	fakeMeta.Repository.Provider = "github"
	fakeMeta.Repository.Branch = "main" // Updated to match the new repository's default branch
}
