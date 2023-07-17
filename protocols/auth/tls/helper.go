//go:build !odo

package tls

import (
	_ "embed"
	"os"

	"github.com/taubyte/go-interfaces/services/common"
)

//go:embed fullchain.pem
var fullchainPEM []byte

//go:embed privkey.pem
var keyPEM []byte

func WriteCerts() {
	os.WriteFile(common.DefaultCAFileName, fullchainPEM, 0400)
	os.WriteFile(common.DefaultKeyFileName, keyPEM, 0400)
}
