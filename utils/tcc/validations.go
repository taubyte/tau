package tccUtils

import (
	"fmt"
	"regexp"

	dv "github.com/taubyte/domain-validation"
	domainSpec "github.com/taubyte/tau/legacy/specs/domain"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// THE VALIDATOR IS THE CONTRACT; THE KEY IS THE VOCABULARY'S OWN WORD.
//
// EmitValidation(key, validator) says two different things. `validator` NAMES THE
// CHECK the compiler is asking this consumer to run — "dns", "project_id" — and that
// is the seam between a vocabulary and something that knows how to verify. `key` is
// the vocabulary's own name for the field it came from, which v1 spells `domain` and
// v2 spells `zone`, because v1 has a `domain` kind and v2 has a `zone`.
//
// Matching on the key was therefore a consumer hardcoding a vocabulary — the defect
// v2's AGENTS.md §8c is about — and it had already bitten: v2 emits Key "zone", this
// matched Key "domain", so a v2 zone published with NO DNS PROOF AT ALL and said
// nothing. Both clauses go, not only the one that was wrong: `project_id` agrees
// today by coincidence of naming, and a rule that holds only while two vocabularies
// happen to pick the same word is not a rule.
//
// ExtractProjectID returns the project ID from validations (no validation performed).
func ExtractProjectID(validations []engine.NextValidation) (string, error) {
	for _, validation := range validations {
		if validation.Validator == "project_id" {
			projectID, ok := validation.Value.(string)
			if !ok {
				return "", fmt.Errorf("project ID validation value is not a string: %T", validation.Value)
			}
			return projectID, nil
		}
	}
	return "", fmt.Errorf("project ID validation not found")
}

// ProcessProjectIDValidation runs project ID validation from TCC compiler.
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

// ProcessDNSValidations runs DNS validations from TCC compiler.
func ProcessDNSValidations(
	validations []engine.NextValidation,
	generatedDomainRegExp *regexp.Regexp,
	devMode bool,
	dvPublicKey []byte,
) error {
	for _, validation := range validations {
		if validation.Validator == "dns" {
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

// Deferred is what a compile could not check, grouped by the validator it asked
// for, with each distinct value once and in order.
//
// THE SCHEMA CANNOT CHECK A REGION AND NEVER WILL. A region name is an opaque
// label the implementation defines: the config holds no list of them, so
// "eu-wst" is refusable only where the cloud is finally in the room. The same
// is true of a domain nobody has proved and an image nobody has pulled.
//
// SO THE HONEST THING IS TO SAY SO. A compile that reports nothing about these
// reads as "your config is checked", and the first time an author learns their
// region label means nothing is a deploy that placed their database somewhere
// they did not write. This does not check them — nothing here can — it names
// what is outstanding and who has to answer it.
func Deferred(validations []engine.NextValidation) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, v := range validations {
		value, ok := v.Value.(string)
		if !ok || value == "" {
			continue
		}
		if at := v.Validator + "\x00" + value; !seen[at] {
			seen[at] = true
			out[v.Validator] = append(out[v.Validator], value)
		}
	}
	return out
}
