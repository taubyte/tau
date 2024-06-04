package database

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Context struct {
	ProjectId     string
	ApplicationId string
	Matcher       string
	Config        *structureSpec.Database
}
