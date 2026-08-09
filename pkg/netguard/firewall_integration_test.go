//go:build linux && firewall_integration

// Integration tests for the nftables egress firewall. They program the real
// host firewall and manipulate cgroups, so they need CAP_NET_ADMIN, the cgroup
// v2 unified hierarchy, and kernel >= 5.13 (socket cgroupv2 match). Run inside
// the disposable rootful VM, not on a dev host:
//
//	go test -tags firewall_integration -c -o \
//	  pkg/containers/backends/containerd/vagrant/netguard.test ./pkg/netguard/
//	(cd pkg/containers/backends/containerd/vagrant && vagrant up)
//	vagrant ssh -c "sudo /vagrant/netguard.test -test.v"
package netguard

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/google/nftables"
	"golang.org/x/sys/unix"
)

func delTestTable(t *testing.T) {
	c, err := nftables.New()
	if err != nil {
		return
	}
	tables, _ := c.ListTablesOfFamily(nftables.TableFamilyINet)
	for _, tbl := range tables {
		if tbl.Name == tableName {
			c.DelTable(tbl)
		}
	}
	if err := c.Flush(); err != nil {
		t.Logf("cleanup: del table: %v", err)
	}
}

// The installers must program nftables without error under CAP_NET_ADMIN, and
// the deny sets must land with elements. This is also the regression test for
// the element encoding: the kernel rejects the library's Key/KeyEnd interval
// form with EINVAL, so deniedElements emits boundary pairs instead.
func TestInstall_Integration(t *testing.T) {
	if err := InstallBridgeFilter("docker0"); err != nil {
		t.Fatalf("InstallBridgeFilter: %v", err)
	}
	t.Cleanup(func() { delTestTable(t) })

	c, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tbl *nftables.Table
	for _, x := range tables {
		if x.Name == tableName {
			tbl = x
		}
	}
	if tbl == nil {
		t.Fatal("table not installed")
	}
	for _, name := range []string{setV4, setV6} {
		s, err := c.GetSetByName(tbl, name)
		if err != nil {
			t.Fatalf("get set %s: %v", name, err)
		}
		els, err := c.GetSetElements(s)
		if err != nil {
			t.Fatalf("get elements %s: %v", name, err)
		}
		if len(els) == 0 {
			t.Errorf("set %s has no elements", name)
		}
	}
}

// The definitive check: a socket in a cgroup under the matched parent must be
// dropped when it dials a denied (loopback) destination, and must still reach a
// permitted one. This validates the `socket cgroupv2 level N` parent match —
// the piece that installs cleanly but could match nothing (fail-open), or
// everything (drops the build's legitimate downloads).
func TestCgroupEgressDrop_Integration(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	target := ln.Addr().String()

	// Baseline: from the root cgroup, the loopback dial succeeds.
	if c, err := net.DialTimeout("tcp", target, 2*time.Second); err != nil {
		t.Fatalf("baseline dial to %s failed before any rule: %v", target, err)
	} else {
		c.Close()
	}
	// A permitted destination to prove the rule is not a blanket drop. Skipped
	// rather than failed when the VM has no outbound internet.
	const public = "1.1.1.1:443"
	publicOK := false
	if c, err := net.DialTimeout("tcp", public, 3*time.Second); err == nil {
		c.Close()
		publicOK = true
	} else {
		t.Logf("no outbound internet (%v); skipping the permitted-destination half", err)
	}

	// A second canary on an address of this host outside every denied CIDR.
	var localTarget string
	const localAddr = "203.0.113.9"
	if err := exec.Command("ip", "addr", "add", localAddr+"/32", "dev", "lo").Run(); err != nil {
		t.Logf("could not add %s to lo (%v); skipping the host-address half", localAddr, err)
	} else {
		t.Cleanup(func() { exec.Command("ip", "addr", "del", localAddr+"/32", "dev", "lo").Run() })
		localLn, err := net.Listen("tcp", localAddr+":0")
		if err != nil {
			t.Fatalf("listening on %s: %v", localAddr, err)
		}
		defer localLn.Close()
		go func() {
			for {
				conn, err := localLn.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}()
		localTarget = localLn.Addr().String()
		if c, err := net.DialTimeout("tcp", localTarget, 2*time.Second); err != nil {
			t.Fatalf("baseline dial to %s failed before any rule: %v", localTarget, err)
		} else {
			c.Close()
		}
	}

	const parent = "/sys/fs/cgroup/ngtest"
	const child = parent + "/child"
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir cgroup: %v", err)
	}
	pid := strconv.Itoa(os.Getpid())
	t.Cleanup(func() {
		os.WriteFile("/sys/fs/cgroup/cgroup.procs", []byte(pid), 0o644) // move back to root
		os.Remove(child)
		os.Remove(parent)
	})

	var st unix.Stat_t
	if err := unix.Stat(parent, &st); err != nil {
		t.Fatalf("stat %s: %v", parent, err)
	}
	// parent "/ngtest" is one component under the cgroup root => level 1.
	if err := InstallCgroupFilter(st.Ino, 1); err != nil {
		t.Fatalf("InstallCgroupFilter: %v", err)
	}
	t.Cleanup(func() { delTestTable(t) })

	// Join the child cgroup; new sockets now belong to it.
	if err := os.WriteFile(child+"/cgroup.procs", []byte(pid), 0o644); err != nil {
		t.Fatalf("join cgroup: %v", err)
	}

	c, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err == nil {
		c.Close()
		t.Fatal("dial to a denied (loopback) destination SUCCEEDED from the filtered cgroup — firewall failed open")
	}
	t.Logf("dial correctly blocked from the filtered cgroup: %v", err)

	// An address of this host that no denied CIDR covers: only the rule that
	// asks the routing table for the destination type can stop this one, and
	// that is the rule standing between a node with a public IP and every
	// service it binds to 0.0.0.0.
	if localTarget != "" {
		if c, err := net.DialTimeout("tcp", localTarget, 2*time.Second); err == nil {
			c.Close()
			out, _ := exec.Command("nft", "list", "table", "inet", tableName).CombinedOutput()
			t.Errorf("dial to %s (this host's own address, in no denied CIDR) SUCCEEDED from the filtered cgroup.\nruleset:\n%s", localTarget, out)
		} else {
			t.Logf("dial to this host's own %s correctly blocked: %v", localTarget, err)
		}
	}

	if publicOK {
		if c, err := net.DialTimeout("tcp", public, 5*time.Second); err != nil {
			t.Errorf("dial to a permitted destination %s was blocked from the filtered cgroup: %v", public, err)
		} else {
			c.Close()
		}
	}
}
