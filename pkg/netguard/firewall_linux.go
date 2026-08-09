//go:build linux

package netguard

import (
	"bytes"
	"fmt"
	"net"
	"sort"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// tableName is the single inet table holding the egress-deny sets and chains.
const tableName = "taubyte_netguard"

const (
	setV4 = "deniedV4"
	setV6 = "deniedV6"
)

// lastAddr returns the last (broadcast) address of the range described by
// ip/mask — the inclusive upper bound of the interval.
func lastAddr(ip net.IP, mask net.IPMask) net.IP {
	end := make(net.IP, len(ip))
	for i := range ip {
		end[i] = ip[i] | ^mask[i]
	}
	return end
}

// nextAddr returns ip+1, or false if ip is the last address of its family (the
// increment wrapped), which has no successor to mark an interval's end with.
func nextAddr(ip net.IP) (net.IP, bool) {
	out := make(net.IP, len(ip))
	copy(out, ip)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			return out, true
		}
	}
	return nil, false
}

// deniedRanges returns the denied CIDRs of one address family as inclusive
// [start, end] ranges, sorted and merged. Merging is required, not cosmetic:
// the kernel stores an interval set as a set of boundaries, so two adjacent
// ranges (::/128 and ::1/128 here) would emit an end marker and a start element
// carrying the same key — a duplicate the kernel rejects.
func deniedRanges(v6 bool) [][2]net.IP {
	var rs [][2]net.IP
	for _, p := range deniedCIDRs {
		isV6 := len(p.Mask) != net.IPv4len
		if isV6 != v6 {
			continue
		}
		start := p.IP
		if !v6 {
			start = start.To4()
		}
		rs = append(rs, [2]net.IP{start, lastAddr(start, p.Mask)})
	}
	sort.Slice(rs, func(i, j int) bool { return bytes.Compare(rs[i][0], rs[j][0]) < 0 })

	merged := rs[:0:0]
	for _, r := range rs {
		if n := len(merged); n > 0 {
			// Adjacent counts as overlapping: [a,b] and [b+1,c] are one interval.
			if after, ok := nextAddr(merged[n-1][1]); !ok || bytes.Compare(r[0], after) <= 0 {
				if bytes.Compare(r[1], merged[n-1][1]) > 0 {
					merged[n-1][1] = r[1]
				}
				continue
			}
		}
		merged = append(merged, r)
	}
	return merged
}

// deniedElements builds the interval-set elements for one address family from
// the shared policy. Each range is a pair of boundaries — the start address,
// then an exclusive end marker at end+1 — which is what nft itself emits. The
// Key/KeyEnd form the library also offers is rejected by the kernel with EINVAL
// (verified on 6.8, see firewall_integration_test.go). A range ending at the
// last address of the family emits no end marker: it runs to the top.
func deniedElements(v6 bool) []nftables.SetElement {
	var els []nftables.SetElement
	for _, r := range deniedRanges(v6) {
		els = append(els, nftables.SetElement{Key: []byte(r[0])})
		if end, ok := nextAddr(r[1]); ok {
			els = append(els, nftables.SetElement{Key: []byte(end), IntervalEnd: true})
		}
	}
	return els
}

// ifname pads an interface name to IFNAMSIZ, as nftables compares iifname
// against a fixed-width, NUL-padded buffer.
func ifname(name string) []byte {
	b := make([]byte, unix.IFNAMSIZ)
	copy(b, name)
	return b
}

// installTable programs the whole taubyte_netguard table in a single netlink
// transaction: any pre-existing table of the same name is deleted and the new
// one created in the same batch, which nftables applies atomically — there is
// no window in which the filter is absent (a del+add of a table replaces it in
// place). addChainsAndRules appends the backend-specific chains and rules to the
// same batch before the single Flush.
//
// The returned error is the fail-closed signal: EPERM means the tau process
// lacks CAP_NET_ADMIN.
func installTable(addChainsAndRules func(c *nftables.Conn, t *nftables.Table, v4, v6 *nftables.Set)) error {
	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("opening nftables netlink connection: %w", err)
	}

	// Immediate (non-batched) query; queue a delete for our table if it exists.
	if existing, err := c.ListTablesOfFamily(nftables.TableFamilyINet); err == nil {
		for _, t := range existing {
			if t.Name == tableName {
				c.DelTable(t)
			}
		}
	}

	// No AutoMerge: it only writes an nft display hint into set userdata, the
	// kernel does not act on it — deniedElements merges the ranges itself.
	t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: tableName})
	v4 := &nftables.Set{Table: t, Name: setV4, Interval: true, KeyType: nftables.TypeIPAddr}
	if err := c.AddSet(v4, deniedElements(false)); err != nil {
		return fmt.Errorf("adding %s set: %w", setV4, err)
	}
	v6 := &nftables.Set{Table: t, Name: setV6, Interval: true, KeyType: nftables.TypeIP6Addr}
	if err := c.AddSet(v6, deniedElements(true)); err != nil {
		return fmt.Errorf("adding %s set: %w", setV6, err)
	}

	addChainsAndRules(c, t, v4, v6)

	if err := c.Flush(); err != nil {
		return fmt.Errorf("installing egress firewall (needs CAP_NET_ADMIN): %w", err)
	}
	return nil
}

// Uninstall removes the egress table, if present. Nothing in the node calls it
// — the policy is static and re-asserted per container — but a test that programs
// the host firewall must be able to put the host back.
func Uninstall() error {
	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("opening nftables netlink connection: %w", err)
	}
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("listing tables: %w", err)
	}
	for _, t := range tables {
		if t.Name == tableName {
			c.DelTable(t)
		}
	}
	return c.Flush()
}

// dropLocalExprs returns the tail of a rule that drops a packet addressed to
// any address this host holds. The denied CIDRs cover loopback and the private
// ranges, but a node's own routable address is in none of them, and on that
// address every service it binds to 0.0.0.0 is reachable — the node's own, and
// whatever else the operator runs. Asking the routing table for the address
// type covers them all, needs no enumeration, and stays correct when the host's
// addresses change under it. It assumes register 1 is free for reuse.
func dropLocalExprs() []expr.Any {
	return []expr.Any{
		&expr.Fib{Register: 1, FlagDADDR: true, ResultADDRTYPE: true},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(unix.RTN_LOCAL)},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}
}

// dropDeniedExprs returns the tail of a rule that drops a packet whose
// destination address is in the denied set for the given family. It assumes
// register 1 is free for reuse.
func dropDeniedExprs(v6 bool, set *nftables.Set) []expr.Any {
	proto := byte(unix.NFPROTO_IPV4)
	offset, length := uint32(16), uint32(4) // IPv4 daddr
	if v6 {
		proto = unix.NFPROTO_IPV6
		offset, length = 24, 16 // IPv6 daddr
	}
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length},
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}
}

// InstallBridgeFilter programs the host firewall so containers attached to the
// named bridge cannot reach denied destinations. Every container on that bridge
// gets a veth into it, so one interface-scoped, static ruleset covers them all —
// install once per process. Idempotent (atomic table replace).
//
// The caller passes the bridge of a network reserved for restricted containers,
// not the shared default one: the rule cannot tell containers apart, so anything
// else on that bridge is filtered too.
func InstallBridgeFilter(bridge string) error {
	return installTable(func(c *nftables.Conn, t *nftables.Table, v4, v6 *nftables.Set) {
		// FORWARD covers container -> external. INPUT covers container -> a host
		// service reached via the bridge gateway IP (delivered locally, not
		// forwarded), which the forward rule alone would miss.
		for _, hk := range []struct {
			name string
			hook *nftables.ChainHook
		}{{"forward", nftables.ChainHookForward}, {"input", nftables.ChainHookInput}} {
			ch := c.AddChain(&nftables.Chain{
				Name:     "egress_" + hk.name,
				Table:    t,
				Type:     nftables.ChainTypeFilter,
				Hooknum:  hk.hook,
				Priority: nftables.ChainPriorityFilter,
			})
			fromBridge := func() []expr.Any {
				return []expr.Any{
					&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(bridge)},
				}
			}
			c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: append(fromBridge(), dropLocalExprs()...)})
			for _, fam := range []struct {
				v6  bool
				set *nftables.Set
			}{{false, v4}, {true, v6}} {
				c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: append(fromBridge(), dropDeniedExprs(fam.v6, fam.set)...)})
			}
		}
	})
}

// InstallCgroupFilter programs a single, static output-hook rule dropping
// denied-destination packets from any socket whose cgroup v2 has, at ancestor
// level `level`, the cgroup identified by cgroupID. Used on the containerd
// backend, where containers share the host network namespace (no bridge to
// match on) but restricted ones all run under one parent cgroup: matching that
// parent with a single rule covers them all, nested cgroups included, without
// any per-container rule churn. Install once per process against the
// pre-created parent cgroup.
//
// A wrong level or id installs cleanly but matches nothing — it fails *open* —
// so the match is exercised end to end by TestCgroupEgressDrop_Integration: a
// process in a cgroup below the matched parent must be refused a loopback
// destination and still reach a public one. Requires kernel >= 5.13 and
// CAP_NET_ADMIN; the level is counted from the cgroup root, so a parent at
// /sys/fs/cgroup/<ns>/restricted is level 2.
func InstallCgroupFilter(cgroupID uint64, level uint32) error {
	return installTable(func(c *nftables.Conn, t *nftables.Table, v4, v6 *nftables.Set) {
		ch := c.AddChain(&nftables.Chain{
			Name:     "egress_output",
			Table:    t,
			Type:     nftables.ChainTypeFilter,
			Hooknum:  nftables.ChainHookOutput,
			Priority: nftables.ChainPriorityFilter,
		})
		inCgroup := func() []expr.Any {
			return []expr.Any{
				&expr.Socket{Key: expr.SocketKeyCgroupv2, Level: level, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint64(cgroupID)},
			}
		}
		c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: append(inCgroup(), dropLocalExprs()...)})
		for _, fam := range []struct {
			v6  bool
			set *nftables.Set
		}{{false, v4}, {true, v6}} {
			c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: append(inCgroup(), dropDeniedExprs(fam.v6, fam.set)...)})
		}
	})
}
