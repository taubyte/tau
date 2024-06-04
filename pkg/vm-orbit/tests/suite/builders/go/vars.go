package goBuilder

import _ "embed"

//go:generate /bin/bash mkfixtures.sh

//go:embed fixtures.tar
var fixture []byte
