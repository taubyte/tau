package seer

import (
	"errors"

	"github.com/taubyte/tau/core/services/tns"
	domainSpecs "github.com/taubyte/tau/pkg/specs/domain"
)

func (h *dnsHandler) fetchDomainTnsPathSlice(name string) ([]string, error) {
	tnsPathSlice, err := domainSpecs.Tns().BasicPath(name)
	if err != nil {
		return nil, errors.New("invalid domain")
	}

	tnsInterface, err := h.seer.tns.Lookup(tns.Query{
		Prefix: tnsPathSlice.Slice(),
		RegEx:  false,
	})
	if err != nil {
		return nil, errors.New("domain not registred")
	}

	domPath, ok := tnsInterface.([]string)
	if !ok || len(domPath) == 0 {
		return nil, errors.New("invalid domain entry in tns")
	}

	h.seer.positiveCache.Set(name, domPath, PositiveCacheTTL)
	return domPath, nil
}
