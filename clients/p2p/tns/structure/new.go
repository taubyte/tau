package structure

import (
	"github.com/taubyte/go-interfaces/services/tns"
	commonSpec "github.com/taubyte/go-specs/common"
	structureSpec "github.com/taubyte/go-specs/structure"
)

func New[T structureSpec.Structure](tns tns.Client, variable commonSpec.PathVariable) tns.StructureIface[T] {
	return &Structure[T]{tns, variable}
}
