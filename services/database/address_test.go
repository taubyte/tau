package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestASTORENAMECARRIESTHEINSTANCE, because a store whose config says it holds
// many is many stores: two instances of one declaration must not be one set of
// bytes.
func TestAnInstanceIsPartOfTheName(t *testing.T) {
	one := address{project: "shop", environment: "prod", id: "QmSessions"}
	many := address{project: "shop", environment: "prod", id: "QmSessions", instance: "eu-1"}

	require.Equal(t, "QmSessions", one.storeName())
	require.Equal(t, "QmSessions-eu-1", many.storeName())
	require.NotEqual(t, one.key(), many.key())
	require.Equal(t, "shop/prod/QmSessions-eu-1", many.key())
}
