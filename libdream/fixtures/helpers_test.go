package fixtures

import (
	commonTest "github.com/taubyte/tau/libdream/helpers"
)

var (
	fakeMeta = commonTest.ConfigRepo.HookInfo
)

func init() {
	fakeMeta.Repository.Provider = "github"
	fakeMeta.Repository.Branch = "master"
}
