package promptsI18n

import "fmt"

var (
	invalidType = "invalid type `%s`; expected one of: %v"
)

func InvalidType(_type string, types []string) error {
	return fmt.Errorf(invalidType, _type, types)
}
