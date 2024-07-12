package peer

import logging "github.com/ipfs/go-log/v2"

const UserAgent string = "Taubyte Node v1.0"

var logger = logging.Logger("p2p.peer")

var MaxBootstrapNodes = 5
