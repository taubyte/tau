package helpers

import (
	"github.com/ipfs/go-datastore"

	pebble "github.com/ipfs/go-ds-pebble"
)

func NewDatastore(path string) (datastore.Batching, error) {
	return pebble.NewDatastore(path, nil)
}
