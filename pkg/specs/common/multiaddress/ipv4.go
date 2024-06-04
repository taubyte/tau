package multiaddress

import "fmt"

func FromIPv4(address, id string, port int) string {
	return fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", address, port, id)
}
