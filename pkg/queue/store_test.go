package queue

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEscape(t *testing.T) {
	type testCase struct{ in, want string }
	for _, tc := range []testCase{
		{"job-1", "job-1"},
		{"a_b", "a_b"},
		{"", ""},
		{"a/b", "a%2Fb"},
		{".", "%2E"},
		{"..", "%2E%2E"},
		{"%", "%25"},
		{"a%2Fb", "a%252Fb"},
		{" ", "%20"},
		{"\x00", "%00"},
	} {
		if got := escape(tc.in); got != tc.want {
			t.Errorf("escape(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEscapeProducesASafeSegment(t *testing.T) {
	inputs := []string{"a/b", "a%2Fb", ".", "..", "", "x", "%", "//", "a.b", "\xff\xfe", "ready"}
	seen := map[string]string{}
	for _, in := range inputs {
		out := escape(in)
		if strings.Contains(out, "/") {
			t.Errorf("escape(%q) = %q contains a separator", in, out)
		}
		if out == "." || out == ".." {
			t.Errorf("escape(%q) = %q is a path element", in, out)
		}
		if in != "" && out == "" {
			t.Errorf("escape(%q) is empty", in)
		}
		if other, clash := seen[out]; clash {
			t.Errorf("escape(%q) and escape(%q) are both %q", in, other, out)
		}
		seen[out] = in
	}
}

func TestStampIsOrderedAndFixedWidth(t *testing.T) {
	base := time.Unix(0, 1)
	type testCase struct{ a, b time.Time }
	for _, tc := range []testCase{
		{base, base.Add(time.Nanosecond)},
		{base, base.Add(time.Hour)},
		{time.Unix(0, 0), base},
	} {
		sa, sb := stamp(tc.a), stamp(tc.b)
		if len(sa) != stampWidth || len(sb) != stampWidth {
			t.Fatalf("stamp width: %q, %q", sa, sb)
		}
		if !(sa < sb) {
			t.Fatalf("stamp(%s)=%q is not below stamp(%s)=%q", tc.a, sa, tc.b, sb)
		}
	}
}

func TestStampClampsBelowTheEpoch(t *testing.T) {
	if got := stamp(time.Unix(0, -1)); got != strings.Repeat("0", stampWidth) {
		t.Fatalf("stamp: got %q, want zero", got)
	}
}

func TestStampCoversTheEndOfTime(t *testing.T) {
	if got := stamp(addClamped(time.Unix(0, 0), maxDuration)); len(got) != stampWidth {
		t.Fatalf("stamp: got %q, want %d digits", got, stampWidth)
	}
}

func TestKeyspaceRoundTrip(t *testing.T) {
	k := newKeyspace("build/one")
	at := time.Unix(0, 1234)

	ready := k.ready(at, "job/1")
	parsed, err := k.parse(ready)
	if err != nil {
		t.Fatalf("parse(%q): %v", ready, err)
	}
	if parsed.state != readySegment || !parsed.at.Equal(at) {
		t.Fatalf("parse: got (%q, %s), want (%q, %s)", parsed.state, parsed.at, readySegment, at)
	}

	claimed := k.claimed(at, "job/1")
	parsed, err = k.parse(claimed)
	if err != nil {
		t.Fatalf("parse(%q): %v", claimed, err)
	}
	if parsed.state != claimedSegment {
		t.Fatalf("parse: got %q, want %q", parsed.state, claimedSegment)
	}

	if !strings.HasPrefix(ready, k.readyPrefix()) || !strings.HasPrefix(claimed, k.claimedPrefix()) {
		t.Fatal("a key is not under the prefix it is listed by")
	}
	if strings.HasPrefix(k.index("job/1"), k.readyPrefix()) {
		t.Fatal("the index shares a listing with the ready side")
	}
}

func TestParseRejects(t *testing.T) {
	k := newKeyspace("build")
	for _, key := range []string{
		"",
		"elsewhere/build/ready/00000000000000000001/job",
		"queue/other/ready/00000000000000000001/job",
		"queue/build/index/job",
		"queue/build/ready/00000000000000000001",
		"queue/build/ready/00000000000000000001/job/more",
		"queue/build/ready/0000000000000000000x/job",
		"queue/build/ready/-0000000000000000001/job",
	} {
		if _, err := k.parse(key); !errors.Is(err, ErrForeignKey) {
			t.Errorf("parse(%q): got %v, want ErrForeignKey", key, err)
		}
	}
}

func TestRecordRoundTrip(t *testing.T) {
	in := record{ID: "job-1", Data: []byte("payload"), Attempts: 3, DeliverAt: 42, Deadline: 43, Token: "t"}
	raw, err := in.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodeRecord("k", raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID != in.ID || string(out.Data) != string(in.Data) || out.Attempts != in.Attempts ||
		out.DeliverAt != in.DeliverAt || out.Deadline != in.Deadline || out.Token != in.Token {
		t.Fatalf("round trip: got %+v, want %+v", out, in)
	}
}
