//go:build !odo

package service

import (
	_ "embed"

	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"
)

func domainValidationNew(fqdn string, project cid.Cid, privKey, pubKey []byte) (*dv.Claims, error) {
	return nil, nil
}
