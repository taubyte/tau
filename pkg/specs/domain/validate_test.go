package domainSpec

import (
	"regexp"
	"testing"
)

func TestValidate(t *testing.T) {
	err := ValidateDNS(regexp.MustCompile(`^[^.]+\.g\.tau\.link$`), "test-id", "00test-id.g.tau.link", true)
	if err != nil {
		t.Error(err)
		return
	}
}
