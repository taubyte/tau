package options

import (
	"crypto/tls"
	"testing"

	"github.com/taubyte/http/options"
)

type MockConfigurable struct {
	values []interface{}
}

func newMockConfigurable() *MockConfigurable {
	return &MockConfigurable{values: make([]interface{}, 0)}
}

func (m *MockConfigurable) SetOption(o interface{}) error {
	m.values = append(m.values, o)
	return nil
}

func TestCustomDomainChecker(t *testing.T) {
	mc := newMockConfigurable()

	testChecker := func(hello *tls.ClientHelloInfo) bool {
		return true
	}

	err := options.Parse(mc, []options.Option{CustomDomainChecker(testChecker)})
	if err != nil {
		t.Error(err)
		return
	}
	for _, o := range mc.values {
		if _o, ok := o.(OptionChecker); ok == true && _o.Checker != nil && _o.Checker(nil) == true {
			return
		}
	}
	t.Errorf("Option CustomDomainChecker not set correctly")
}
