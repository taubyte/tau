package mocks

import (
	"github.com/stretchr/testify/mock"
)

type WriteCloser struct {
	mock.Mock
}

func (mwc *WriteCloser) Write(p []byte) (n int, err error) {
	args := mwc.Called(p)
	return args.Int(0), args.Error(1)
}

func (mwc *WriteCloser) Close() error {
	args := mwc.Called()
	return args.Error(0)
}
