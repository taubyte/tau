package cm

import (
	"context"
	"io"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	pkgcache "github.com/taubyte/tau/pkg/cache"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/secrets"
)

var clientLog = logging.Logger("tau.cm.client")

// issuing is what a service reads off its node about the cloud's certificates.
type issuing struct {
	CloudFqdn string `yaml:"cloudfqdn,omitempty"`
	CM        Config `yaml:"cm,omitempty"`
}

func issuingOn(h service.Host) (issuing, error) {
	var at issuing
	err := h.Config(&at)
	return at, err
}

// ClientOn is the certificates a service on h serves its public listener with: a
// client over the cm nodes h's discovery finds, presenting DNS-01 values in the
// node's copy of the challenge store and proving a custom zone through the cm nodes.
// Nil in a cloud that issues none. Closing it closes what it opened.
func ClientOn(ctx context.Context, h service.Host) (*Client, error) {
	at, err := issuingOn(h)
	if err != nil || !at.CM.Certificate.ACME.Enabled {
		return nil, err
	}
	if at.CloudFqdn == "" {
		return nil, ErrNoZone
	}
	challenges, err := ChallengesOn(ctx, h)
	if err != nil {
		return nil, err
	}
	domains, err := NewDomains(h.Peer().Host(), On(h))
	if err != nil {
		challenges.Close()
		return nil, err
	}
	vault := secrets.VaultOn(h)
	cfg := at.CM
	cfg.Certificate.Zone = at.CloudFqdn
	c, err := NewClient(h.Peer().Host(), On(h), vault.Cloud(VaultService), cfg,
		WithLogger(clientLog.Warnf), WithChallengeStore(challenges), WithProver(domains))
	if err != nil {
		domains.Close()
		vault.Close()
		challenges.Close()
		return nil, err
	}
	c.also = []io.Closer{domains, vault, challenges}
	return c, nil
}

// ChallengesOn is h's node's copy of the cloud's DNS-01 values: what its dns server
// answers while a certificate is proved, and where a client presents them. Nil in a
// cloud that issues none. The store is the node's when it shares its databases, and
// otherwise one opened here and closed with the challenges.
func ChallengesOn(ctx context.Context, h service.Host) (*Challenges, error) {
	at, err := issuingOn(h)
	if err != nil || !at.CM.Certificate.ACME.Enabled {
		return nil, err
	}
	var also []io.Closer
	var db kvdb.Instance
	if dbs, ok := service.Get[service.Databases](h); ok {
		db, err = dbs.Database(ChallengeDatabase)
	} else if db, err = pkgkvdb.Open(h.Peer(), ChallengeDatabase); err == nil {
		also = append(also, closer(func() error { db.Close(); return nil }))
	}
	if err != nil {
		return nil, err
	}
	c, err := ChallengesIn(ctx, pkgcache.Over(db), h.Now)
	if err != nil {
		for _, a := range also {
			a.Close()
		}
		return nil, err
	}
	c.also = also
	return c, nil
}
