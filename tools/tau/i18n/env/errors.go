package envI18n

import "errors"

var (
	ErrorUserNotFound    = errors.New("user not found")
	ErrorProjectNotFound = errors.New("project not found")
)
