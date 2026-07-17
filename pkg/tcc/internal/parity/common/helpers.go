package common

import (
	"strings"

	librarySpec "github.com/taubyte/tau/pkg/specs/library"
)

func LibraryFromSource(source string) string {
	if strings.HasPrefix(source, librarySpec.PathVariable.String()) {
		return strings.TrimPrefix(source, librarySpec.PathVariable.String()+"/")
	}

	return ""
}
