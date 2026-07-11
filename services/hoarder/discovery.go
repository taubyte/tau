package hoarder

// Liveness comes from the heartbeat membership controller (membership.go), not
// from gossipsub topic peers: ListPeers is mesh membership that flaps under load
// and diverges between nodes, which made placement thrash. activeMembers() is a
// stable, per-node-consistent live set.

// fleetSize is the number of live hoarders (>=1, counting self).
func (srv *Service) fleetSize() int {
	return len(srv.activeMembers())
}

// liveClaimants returns the claimants that are currently live members — the live
// replica set. Registry claims can name dead peers; this crosses them with the
// live fleet.
func (srv *Service) liveClaimants(claims []string) []string {
	active := make(map[string]bool, 8)
	for _, m := range srv.activeMembers() {
		active[m] = true
	}
	out := make([]string, 0, len(claims))
	for _, c := range claims {
		if active[c] {
			out = append(out, c)
		}
	}
	return out
}
