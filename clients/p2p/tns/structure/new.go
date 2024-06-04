package structure

import (
	"github.com/taubyte/tau/core/services/tns"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New[T structureSpec.Structure](tns tns.Client, variable commonSpec.PathVariable) tns.StructureIface[T] {
	return &Structure[T]{tns, variable}
}
