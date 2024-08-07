package structure

// TODO: This needs to be refactored with the tns/mocks

import (
	"github.com/taubyte/tau/core/services/tns"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Structure[T structureSpec.Structure] struct {
	tns      tns.Client
	variable commonSpec.PathVariable
}

type simpleClient struct {
	Structure[*structureSpec.Simple]
}

func Simple(tns tns.Client) tns.SimpleIface {
	return &simpleClient{Structure: Structure[*structureSpec.Simple]{tns: tns, variable: ""}}
}

type AllClient[T structureSpec.Structure] struct {
	*Structure[T]
	projectId, appId string
	branches         []string
}

type RelativeClient[T structureSpec.Structure] struct {
	*Structure[T]
	projectId, appId string
	branches         []string
}

type GlobalClient[T structureSpec.Structure] struct {
	*Structure[T]
	projectId string
	branches  []string
}
