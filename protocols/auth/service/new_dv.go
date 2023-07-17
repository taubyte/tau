//go:build !odo

package service

import (
	_ "embed"

	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"
)

//go:embed domain_private.key
var domainValPrivateKeyData []byte

//go:embed domain_public.key
var domainValPublicKeyData []byte

func domainValidationNew(fqdn string, project cid.Cid, privKey, pubKey []byte) (*dv.Claims, error) {
	return dv.New(dv.FQDN(fqdn), dv.Project(project), dv.PrivateKey(domainValPrivateKeyData), dv.PublicKey(domainValPublicKeyData))
}
