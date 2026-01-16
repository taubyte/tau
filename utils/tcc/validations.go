package tccUtils

import (
	"fmt"
	"regexp"

	dv "github.com/taubyte/domain-validation"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// ExtractProjectID extracts the project ID from validations without validation
// Returns the project ID if found, or an error if not found
func ExtractProjectID(validations []engine.NextValidation) (string, error) {
	for _, validation := range validations {
		if validation.Validator == "project_id" && validation.Key == "project_id" {
			projectID, ok := validation.Value.(string)
			if !ok {
				return "", fmt.Errorf("project ID validation value is not a string: %T", validation.Value)
			}
			return projectID, nil
		}
	}
	return "", fmt.Errorf("project ID validation not found")
}

// ProcessProjectIDValidation processes project ID validation from TCC compiler
// Returns the project ID if validation passes, or an error if validation fails
func ProcessProjectIDValidation(
	validations []engine.NextValidation,
	expectedProjectID string,
) (string, error) {
	projectID, err := ExtractProjectID(validations)
	if err != nil {
		return "", err
	}

	if projectID != expectedProjectID {
		return "", fmt.Errorf("project ids not equal `%s` != `%s`", expectedProjectID, projectID)
	}

	return projectID, nil
}

// ProcessDNSValidations processes DNS validations from TCC compiler
func ProcessDNSValidations(
	validations []engine.NextValidation,
	generatedDomainRegExp *regexp.Regexp,
	devMode bool,
	dvPublicKey []byte,
) error {
	for _, validation := range validations {
		if validation.Validator == "dns" && validation.Key == "domain" {
			fqdn, ok := validation.Value.(string)
			if !ok {
				return fmt.Errorf("DNS validation value is not a string: %T", validation.Value)
			}

			projectID, ok := validation.Context["project"].(string)
			if !ok {
				return fmt.Errorf("DNS validation context missing project: %v", validation.Context)
			}

			var err error
			if devMode {
				// In dev mode, DNS validation is skipped (only checks generated domain format)
				err = domainSpec.ValidateDNS(
					generatedDomainRegExp,
					projectID,
					fqdn,
					true, // dev mode - skips DNS validation
				)
			} else {
				// Use provided DV public key for production DNS validation
				err = domainSpec.ValidateDNS(
					generatedDomainRegExp,
					projectID,
					fqdn,
					false, // production mode - performs DNS validation
					dv.PublicKey(dvPublicKey),
				)
			}

			if err != nil {
				return fmt.Errorf("DNS validation failed for %s: %w", fqdn, err)
			}
		}
	}
	return nil
}
