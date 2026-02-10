package domainI18n_test

import (
	"errors"
	"testing"

	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := domainI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "selecting a domain prompt failed")
}

func TestInvalidProjectIDEight(t *testing.T) {
	err := domainI18n.InvalidProjectIDEight("short")
	assert.ErrorContains(t, err, "invalid project ID")
	assert.ErrorContains(t, err, "short")
}

func TestNewDomainValidatorFailed(t *testing.T) {
	err := domainI18n.NewDomainValidatorFailed("mydomain", errors.New("validator err"))
	assert.ErrorContains(t, err, "mydomain")
	assert.ErrorContains(t, err, "validator err")
}

func TestValidateFQDNFailed(t *testing.T) {
	err := domainI18n.ValidateFQDNFailed("example.com", errors.New("validate err"))
	assert.ErrorContains(t, err, "example.com")
	assert.ErrorContains(t, err, "validate err")
}

func TestIsGeneratedFQDNFailed(t *testing.T) {
	err := domainI18n.IsGeneratedFQDNFailed("fqdn.com", errors.New("check err"))
	assert.ErrorContains(t, err, "fqdn.com")
	assert.ErrorContains(t, err, "check err")
}
