package auth

import (
	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"
)

func domainValidationNew(fqdn string, project cid.Cid, privKey, pubKey []byte) (*dv.Claims, error) {
	return dv.New(dv.FQDN(fqdn), dv.Project(project), dv.PrivateKey(privKey), dv.PublicKey(pubKey))
}
