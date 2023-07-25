package mocks

import "github.com/taubyte/go-interfaces/services/substrate"

func New(srv substrate.Service) MockedSmartOps {
	return &mockedSmartOps{srv}
}
