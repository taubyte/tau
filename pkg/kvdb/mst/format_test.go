package mst

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	multihash "github.com/multiformats/go-multihash"
	pb "github.com/taubyte/tau/pkg/kvdb/mst/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestLevelVectors(t *testing.T) {
	for k, want := range map[string]uint32{
		"":            0,
		"a":           0,
		"/k/00000000": 0,
		"k3":          1,
	} {
		if got := Level([]byte(k)); got != want {
			t.Errorf("Level(%q) = %d, want %d", k, got, want)
		}
	}
	// The distribution the fanout rests on: about 1 key in 16 is above
	// level 0, 1 in 256 above level 1. At 2^20 keys the rarest count is
	// about 256, so a quarter either way is four standard deviations.
	var above [3]int
	const n = 1 << 20
	for i := range n {
		l := Level(key(i))
		for j := range above {
			if l > uint32(j) {
				above[j]++
			}
		}
	}
	for j, want := range []int{n / 16, n / 256, n / 4096} {
		if above[j] < want*3/4 || above[j] > want*5/4 {
			t.Errorf("%d of %d keys above level %d, want about %d", above[j], n, j, want)
		}
	}
	long := bytes.Repeat([]byte("x"), 1000)
	if Level(long) != Level(bytes.Clone(long)) {
		t.Fatal("Level of a long key is not stable")
	}
}

func TestLevelDoesNotAllocate(t *testing.T) {
	k := []byte("/some/kvdb/key/of/ordinary/length/0123456789")
	if a := testing.AllocsPerRun(1000, func() { Level(k) }); a != 0 {
		t.Fatalf("Level allocates %v times", a)
	}
}

// The roots of fixed key sets, frozen. A change to any of them is a change
// of format profile.
func TestRootVectors(t *testing.T) {
	cases := []struct {
		n    int
		root string
	}{
		{1, "bafybeidrjbgmcsrtsouij5cjeq6sbbgahmrwf7zvcibqgkwnarvxhx3qze"},
		{2, "bafybeiakmzv6he3hrubgfwfqlbkvxzvecux2undmv4zn2amgset6pfdzcq"},
		{100, "bafybeigat3ttrjfk4q3y6fyrfivcsrwfhgdxl737gepswp4pbflk5kcc54"},
		{1000, "bafybeia5qdyhdlxn5jvbpp4qcr6zowroijoldxlo7hfizxva7mphqdvele"},
	}
	for _, c := range cases {
		m := map[string][]byte{}
		for i := range c.n {
			m[string(key(i))] = value(i)
		}
		tr := build(t, newStore(), m)
		if got := tr.Root().String(); got != c.root {
			t.Errorf("%d keys: root %s, want %s", c.n, got, c.root)
		}
	}
	if New(newStore()).Root().Defined() {
		t.Fatal("the empty tree has a root")
	}
}

// A node's exact bytes: the Data protobuf, then the dag-pb block around it,
// with links in key order.
func TestNodeVector(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	// Keys at level 0 around one key of a higher level, so the root has a
	// subtree on each side.
	var hi []byte
	for i := 0; hi == nil; i++ {
		if k := fmt.Appendf(nil, "k%d", i); Level(k) == 1 {
			hi = k
		}
	}
	tr := New(s)
	for _, k := range [][]byte{[]byte("a"), hi, []byte("kz"), []byte("kzz")} {
		tr = put(t, tr, k, []byte("v"))
	}
	nd, err := s.Get(ctx, tr.Root())
	if err != nil {
		t.Fatal(err)
	}
	pn := nd.(*dag.ProtoNode)
	if got, want := hex.EncodeToString(pn.Data()), "0801100118012a0912026b331a01762001"; got != want {
		t.Errorf("root Data %s, want %s", got, want)
	}
	if got, want := hex.EncodeToString(nd.RawData()), "122a0a240170122034f513ac8a4118303a03c97329739d457e10fdaeb43fcfb4dc18d797399dc8f612001800122a0a24017012208b1844235333befd05f9b40e3671bbbe89d26ffc5c56f7af77dfbf83d71012e2120018000a110801100118012a0912026b331a01762001"; got != want {
		t.Errorf("root block %s, want %s", got, want)
	}
	if got, want := tr.Root().String(), "bafybeib3mqaqyaoyw4na4l6wjdcbmgi76mmxgzl2ese2rqd266f64snflq"; got != want {
		t.Errorf("root %s, want %s", got, want)
	}
	if p := tr.Root().Prefix(); p.Version != 1 || p.Codec != cid.DagProtobuf || p.MhType != multihash.SHA2_256 {
		t.Fatalf("root prefix %v", tr.Root().Prefix())
	}
	// Links are in key order: the first is the subtree below hi, the
	// second the one above it, as added.
	links := nd.Links()
	if len(links) != 2 {
		t.Fatalf("%d links", len(links))
	}
	for i, want := range [][]byte{[]byte("a"), []byte("kz")} {
		sub, err := s.Get(ctx, links[i].Cid)
		if err != nil {
			t.Fatal(err)
		}
		n, err := decode(sub, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(n.entries[0].key, want) || links[i].Name != "" || links[i].Size != 0 {
			t.Fatalf("link %d leads to %q (name %q, size %d)", i, n.entries[0].key, links[i].Name, links[i].Size)
		}
	}
	// Re-decoding the block from its bytes gives the same links in the
	// same order.
	re, err := dag.DecodeProtobuf(nd.RawData())
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range re.Links() {
		if l.Cid != links[i].Cid {
			t.Fatalf("decoded link %d is %s, not %s", i, l.Cid, links[i].Cid)
		}
	}
}

// block makes a dag-pb block holding msg and links, the way encode does.
func block(t *testing.T, msg proto.Message, links ...cid.Cid) *dag.ProtoNode {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	nd := dag.NodeWithData(data)
	for _, l := range links {
		if err := nd.AddRawLink("", &ipld.Link{Cid: l}); err != nil {
			t.Fatal(err)
		}
	}
	if err := nd.SetCidBuilder(dag.V1CidPrefix()); err != nil {
		t.Fatal(err)
	}
	return nd
}

func TestDecodeRefuses(t *testing.T) {
	leaf := block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a")}}})
	withUnknown := &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a")}}}
	withUnknown.ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01})
	entryUnknown := &pb.Entry{Suffix: []byte("a")}
	entryUnknown.ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01})
	for name, c := range map[string]struct {
		nd    ipld.Node
		below uint32
		want  error
	}{
		"format 0":            {block(t, &pb.Node{Entries: []*pb.Entry{{Suffix: []byte("a")}}}), noLevel, ErrFormat},
		"format 2":            {block(t, &pb.Node{Format: 2, Entries: []*pb.Entry{{Suffix: []byte("a")}}}), noLevel, ErrFormat},
		"no entries":          {block(t, &pb.Node{Format: 1}), noLevel, ErrMalformed},
		"unknown field":       {block(t, withUnknown), noLevel, ErrMalformed},
		"unknown entry field": {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{entryUnknown}}), noLevel, ErrMalformed},
		"not below parent":    {block(t, &pb.Node{Format: 1, Level: 2, Entries: []*pb.Entry{{Suffix: []byte("a")}}}), 2, ErrMalformed},
		"first shares":        {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Shared: 1, Suffix: []byte("a")}}}), noLevel, ErrMalformed},
		"shares too much":     {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a")}, {Shared: 2, Suffix: []byte("b")}}}), noLevel, ErrMalformed},
		"duplicate":           {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a")}, {Shared: 1}}}), noLevel, ErrDuplicateKey},
		"unsorted":            {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("b")}, {Suffix: []byte("a")}}}), noLevel, ErrUnsorted},
		"missing link":        {block(t, &pb.Node{Format: 1, Left: true, Entries: []*pb.Entry{{Suffix: []byte("a")}}}), noLevel, ErrMalformed},
		"extra link":          {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a")}}}, leaf.Cid()), noLevel, ErrMalformed},
		"orphan summary":      {block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("a"), RightSummary: []byte{1}}}}), noLevel, ErrMalformed},
		"not a node":          {block(t, &emptypb.Empty{}), noLevel, ErrFormat},
		"not protobuf data":   {dag.NodeWithData([]byte{0xff}), noLevel, ErrMalformed},
		"not dag-pb":          {dag.NewRawNode([]byte{0xff}), noLevel, ErrMalformed},
	} {
		if _, err := decode(c.nd, c.below); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if _, err := decode(leaf, noLevel); err != nil {
		t.Fatalf("a well-formed leaf: %v", err)
	}
}

// Verify refuses trees that decode but are not what this package writes.
func TestVerifyRefuses(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	add := func(nd ipld.Node) *Tree {
		t.Helper()
		if err := s.Add(ctx, nd); err != nil {
			t.Fatal(err)
		}
		return Load(s, nd.Cid())
	}
	var lvl1, lvl0 []byte
	for i := 0; lvl1 == nil || lvl0 == nil; i++ {
		k := fmt.Appendf(nil, "k%d", i)
		switch Level(k) {
		case 0:
			lvl0 = k
		case 1:
			lvl1 = k
		}
	}
	v0 := dag.NodeWithData(block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: lvl0}}}).Data())
	child := add(block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: lvl0}}}))
	leftOf := bytes.Compare(lvl0, lvl1) < 0
	summaryIf := func(b bool) []byte {
		if b {
			return []byte{1}
		}
		return nil
	}
	for name, tr := range map[string]*Tree{
		"prefix not the longest": add(block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte("ab")}, {Shared: 0, Suffix: []byte("ac")}}})),
		"wrong level":            add(block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: lvl1}}})),
		"cid v0":                 add(v0),
		"key outside its slot":   add(block(t, &pb.Node{Format: 1, Level: 1, Left: !leftOf, Entries: []*pb.Entry{{Suffix: lvl1, Right: leftOf}}}, child.Root())),
		"summary without summarizer": add(block(t, &pb.Node{Format: 1, Level: 1, Left: leftOf, LeftSummary: summaryIf(leftOf),
			Entries: []*pb.Entry{{Suffix: lvl1, Right: !leftOf, RightSummary: summaryIf(!leftOf)}}}, child.Root())),
		"child absent": add(block(t, &pb.Node{Format: 1, Level: 1, Left: true, Entries: []*pb.Entry{{Suffix: lvl1}}}, leafCid(t, "never added"))),
	} {
		if err := tr.Verify(ctx); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
	if err := child.Verify(ctx); err != nil {
		t.Fatalf("a canonical leaf: %v", err)
	}
}

func leafCid(t *testing.T, k string) cid.Cid {
	return block(t, &pb.Node{Format: 1, Entries: []*pb.Entry{{Suffix: []byte(k)}}}).Cid()
}
