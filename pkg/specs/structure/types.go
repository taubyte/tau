package structureSpec

import "github.com/taubyte/tau/pkg/specs/common"

type Structure interface {
	*App |
		*Database |
		*Domain |
		*Function |
		*Library |
		*Messaging |
		*Service |
		*SmartOp |
		*Storage |
		*Website |

		// Added for usage outside of defined resources
		*Simple

	SimpleIface
}

type SimpleIface interface {
	GetName() string
	GetId() string
	SetId(string)
}

type Basic interface {
	SimpleIface
	BasicPath(branch, commit, project, app string) (*common.TnsPath, error)
}

type Indexer interface {
	SimpleIface
	IndexValue(branch, project, app string) (*common.TnsPath, error)
}

type Wasm interface {
	Basic
	Indexer
	WasmModulePath(project, app string) (*common.TnsPath, error)
	ModuleName() string
}
