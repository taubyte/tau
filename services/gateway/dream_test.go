//go:build dreaming

package gateway_test

import (
	"io"
	"strings"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/gateway/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

func TestBasicPing_Dreaming(t *testing.T) {
	res := testSingleFunction(t, "ping", "GET", "ping.zwasm", nil)
	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "PONG")
}

func TestBasicWithBody_Dreaming(t *testing.T) {
	body := "hello_world"
	res := testSingleFunction(t, "toUpper", "POST", "toupper.zwasm", []byte(body))
	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, string(data), strings.ToUpper(body))
}
