package uuid

import (
	"github.com/google/uuid"
)

// New returns a new UUID as a string
func New() string {
	return uuid.New().String()
}
