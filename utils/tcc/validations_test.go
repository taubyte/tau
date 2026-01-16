package tccUtils

import (
	"regexp"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func TestProcessProjectIDValidation_Success(t *testing.T) {
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"project_id",
			"QmTestProject123",
			"project_id",
			map[string]interface{}{},
		),
	}

	projectID, err := ProcessProjectIDValidation(validations, "QmTestProject123")
	assert.NilError(t, err)
	assert.Equal(t, projectID, "QmTestProject123")
}

func TestProcessProjectIDValidation_Mismatch(t *testing.T) {
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"project_id",
			"QmTestProject123",
			"project_id",
			map[string]interface{}{},
		),
	}

	_, err := ProcessProjectIDValidation(validations, "QmDifferentProject")
	assert.ErrorContains(t, err, "project ids not equal")
}

func TestProcessProjectIDValidation_NotFound(t *testing.T) {
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"example.com",
			"dns",
			map[string]interface{}{
				"project": "QmTestProject123",
			},
		),
	}

	_, err := ProcessProjectIDValidation(validations, "QmTestProject123")
	assert.ErrorContains(t, err, "project ID validation not found")
}

func TestProcessProjectIDValidation_EmptyValidations(t *testing.T) {
	validations := []engine.NextValidation{}

	_, err := ProcessProjectIDValidation(validations, "QmTestProject123")
	assert.ErrorContains(t, err, "project ID validation not found")
}

func TestProcessProjectIDValidation_InvalidType(t *testing.T) {
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"project_id",
			12345, // invalid type - should be string
			"project_id",
			map[string]interface{}{},
		),
	}

	_, err := ProcessProjectIDValidation(validations, "QmTestProject123")
	assert.ErrorContains(t, err, "project ID validation value is not a string")
}

func TestProcessDNSValidations_DevMode_GeneratedDomain(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"test12345678.g.tau.link", // generated domain with project ID suffix
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
	}

	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.NilError(t, err)
}

func TestProcessDNSValidations_DevMode_NonGeneratedDomain(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"example.com", // non-generated domain
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
	}

	// In dev mode, non-generated domains are skipped (no validation)
	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.NilError(t, err)
}

func TestProcessDNSValidations_ProductionMode_GeneratedDomain(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"test12345678.g.tau.link",
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
	}

	// In production, generated domains are validated (check project ID in domain)
	err := ProcessDNSValidations(validations, generatedDomainRegExp, false, []byte("fake-key"))
	assert.NilError(t, err)
}

func TestProcessDNSValidations_InvalidValueType(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			12345, // invalid type - should be string
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
	}

	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.ErrorContains(t, err, "DNS validation value is not a string")
}

func TestProcessDNSValidations_MissingProjectContext(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"example.com",
			"dns",
			map[string]interface{}{}, // missing project
		),
	}

	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.ErrorContains(t, err, "DNS validation context missing project")
}

func TestProcessDNSValidations_InvalidProjectContextType(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"example.com",
			"dns",
			map[string]interface{}{
				"project": 12345, // invalid type - should be string
			},
		),
	}

	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.ErrorContains(t, err, "DNS validation context missing project")
}

func TestProcessDNSValidations_NonDNSValidation(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"project_id",
			"QmTestProject123",
			"project_id",
			map[string]interface{}{},
		),
	}

	// Should not process non-DNS validations
	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.NilError(t, err) // No DNS validations to process, so no error
}

func TestProcessDNSValidations_MultipleValidations(t *testing.T) {
	generatedDomainRegExp := regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	validations := []engine.NextValidation{
		engine.NewNextValidation(
			"domain",
			"test12345678.g.tau.link",
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
		engine.NewNextValidation(
			"domain",
			"another12345678.g.tau.link",
			"dns",
			map[string]interface{}{
				"project": "QmTest12345678",
			},
		),
	}

	err := ProcessDNSValidations(validations, generatedDomainRegExp, true, []byte("fake-key"))
	assert.NilError(t, err)
}
