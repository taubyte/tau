package libraries

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (l *library) WrapError(format string, i ...any) error {
	return fmt.Errorf("on library `"+l.name+"`; "+format, i...)
}

func (l *library) Name() string {
	return l.name
}

func (l *library) Root() *seer.Query {
	return l.Resource.Root()
}

func (l *library) Config() *seer.Query {
	return l.Resource.Config()
}

func (l *library) AppName() string {
	return l.application
}

func (l *library) Directory() string {
	return common.LibraryFolder
}

func (l *library) SetName(name string) {
	l.name = name
}
