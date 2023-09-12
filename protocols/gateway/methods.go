package gateway

func (g *Gateway) threshold() int {
	thresh := int(g.connectedSubstrate)
	if thresh < 1 {
		return 1
	}
	return thresh
}
