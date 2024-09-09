package domainI18n

import "fmt"

const (
	selectPromptFailed       = "selecting a domain prompt failed with: %s"
	invalidProjectIDEight    = "invalid project ID: `%s` < 8 characters"
	newDomainValidatorFailed = "new domain validator for `%s` failed with: %s"
	validateFQDNFailed       = "validating fqdn `%s` failed with: %s"
	isGenereratedFQDNFailed  = "checking if `%s` is a generated fqdn failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}

func InvalidProjectIDEight(projectId string) error {
	return fmt.Errorf(invalidProjectIDEight, projectId)
}

func NewDomainValidatorFailed(name string, err error) error {
	return fmt.Errorf(newDomainValidatorFailed, name, err)
}

func ValidateFQDNFailed(fqdn string, err error) error {
	return fmt.Errorf(validateFQDNFailed, fqdn, err)
}

func IsGeneratedFQDNFailed(fqdn string, err error) error {
	return fmt.Errorf(isGenereratedFQDNFailed, fqdn, err)
}
