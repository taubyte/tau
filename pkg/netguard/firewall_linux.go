//go:build linux

package netguard

import (
	"fmt"
	"net"

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

// deniedElements builds the interval-set elements for one address family from
// the shared policy. Each denied CIDR is one element expressed as an inclusive
// range [network, broadcast] via Key/KeyEnd — the form the google/nftables
// interval-set tests exercise (its IntervalEnd pair form is untested there).
func deniedElements(v6 bool) []nftables.SetElement {
	var els []nftables.SetElement
	for _, p := range deniedCIDRs {
		isV6 := len(p.Mask) != net.IPv4len
		if isV6 != v6 {
			continue
		}
		start := p.IP
		if !v6 {
			start = start.To4()
		}
		els = append(els, nftables.SetElement{
			Key:    []byte(start),
			KeyEnd: []byte(lastAddr(start, p.Mask)),
		})
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

	t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: tableName})
	v4 := &nftables.Set{Table: t, Name: setV4, Interval: true, AutoMerge: true, KeyType: nftables.TypeIPAddr}
	if err := c.AddSet(v4, deniedElements(false)); err != nil {
		return fmt.Errorf("adding %s set: %w", setV4, err)
	}
	v6 := &nftables.Set{Table: t, Name: setV6, Interval: true, AutoMerge: true, KeyType: nftables.TypeIP6Addr}
	if err := c.AddSet(v6, deniedElements(true)); err != nil {
		return fmt.Errorf("adding %s set: %w", setV6, err)
	}

	addChainsAndRules(c, t, v4, v6)

	if err := c.Flush(); err != nil {
		return fmt.Errorf("installing egress firewall (needs CAP_NET_ADMIN): %w", err)
	}
	return nil
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

// InstallDockerBridgeFilter programs the host firewall so build containers on
// the docker bridge cannot reach denied destinations. Docker gives every build
// container a veth into docker0, so one interface-scoped, static ruleset covers
// them all — install once per process. Idempotent (atomic table replace).
func InstallDockerBridgeFilter() error {
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
			for _, fam := range []struct {
				v6  bool
				set *nftables.Set
			}{{false, v4}, {true, v6}} {
				exprs := []expr.Any{
					&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname("docker0")},
				}
				exprs = append(exprs, dropDeniedExprs(fam.v6, fam.set)...)
				c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: exprs})
			}
		}
	})
}

// InstallCgroupFilter programs a single, static output-hook rule dropping
// denied-destination packets from any socket whose cgroup v2 has, at ancestor
// level `level`, the cgroup identified by cgroupID. Used on the containerd
// backend, where build containers share the host network namespace (no bridge
// to match on) but all run under one parent cgroup: matching that parent with a
// single rule covers every build (and BuildKit's nested runc cgroups) without
// any per-container rule churn. Install once per process against the pre-created
// build-namespace cgroup.
//
// VALIDATION REQUIRED on the target kernel: the `socket cgroupv2 level N`
// ancestor-level counting and cgroup-id byte encoding are not verified in the
// dev environment. A wrong level or id installs cleanly but matches nothing —
// i.e. fails *open* — so exercise it in vagrant/CI (a build that curls
// 169.254.169.254 and a loopback canary must be refused; a public fetch must
// pass). Requires kernel >= 5.13 and CAP_NET_ADMIN.
func InstallCgroupFilter(cgroupID uint64, level uint32) error {
	return installTable(func(c *nftables.Conn, t *nftables.Table, v4, v6 *nftables.Set) {
		ch := c.AddChain(&nftables.Chain{
			Name:     "egress_output",
			Table:    t,
			Type:     nftables.ChainTypeFilter,
			Hooknum:  nftables.ChainHookOutput,
			Priority: nftables.ChainPriorityFilter,
		})
		for _, fam := range []struct {
			v6  bool
			set *nftables.Set
		}{{false, v4}, {true, v6}} {
			exprs := []expr.Any{
				&expr.Socket{Key: expr.SocketKeyCgroupv2, Level: level, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint64(cgroupID)},
			}
			exprs = append(exprs, dropDeniedExprs(fam.v6, fam.set)...)
			c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: exprs})
		}
	})
}
