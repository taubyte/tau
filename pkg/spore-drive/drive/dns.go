package drive

import (
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/pkg/spore-drive/config"
)

var logger = logging.Logger("spore-drive")

// logDNSRecords prints the DNS records the cloud needs, relative to the root domain.
func logDNSRecords(p config.Parser) {
	for _, r := range config.DNSRecords(p, "") {
		logger.Infof("DNS record needed: %s %s %s (TTL %d)", r.Name, r.Type, r.Value, r.TTL)
	}
}
