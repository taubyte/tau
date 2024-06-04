package gateway

import (
	"io"
	"strings"
	"testing"

	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"
	"gotest.tools/v3/assert"
)

func TestBasicPing(t *testing.T) {
	res := testSingleFunction(t, "ping", "GET", "ping.zwasm", nil)
	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "PONG")
}

func TestBasicWithBody(t *testing.T) {
	body := "hello_world"
	res := testSingleFunction(t, "toUpper", "POST", "toupper.zwasm", []byte(body))
	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, string(data), strings.ToUpper(body))
}
