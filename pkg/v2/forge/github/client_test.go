package github

// From inside the package: what the driver keeps a credential in. The rest of
// this package's tests are github_test, and this one is not, because the thing
// it asserts is a struct field nobody outside can see — which is exactly why it
// could sit there unnoticed.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheDriverKeepsNoCopyOfTheCredential.
//
// forge.Credential exists to close EVERY way of rendering key material: Format
// catches every verb, and the marshallers fail rather than redact. bind unwraps
// it to a string at the door, and a client that stored that string beside its
// http.Client rendered it under %v like any other field — key material kept in
// the one shape the package went to trouble to make impossible. It was also
// never read: the Authorization header is written by a closure that captures
// the parameter.
func TestTheDriverKeepsNoCopyOfTheCredential(t *testing.T) {
	const secret = "gh-tok-INTERNAL-1a2b3c4d5e6f"
	d := &driver{host: "github.com", apiHost: "api.github.com", base: "https://api.github.com/"}
	c := d.bind(secret)

	for _, rendering := range []string{
		fmt.Sprintf("%v", *c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%v", c),
	} {
		assert.NotContains(t, rendering, secret, "the client rendered its credential: %s", rendering)
	}
}
