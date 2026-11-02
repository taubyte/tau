package kvdb

import (
	"slices"
	"strings"
	"testing"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"gotest.tools/v3/assert"
)

func TestStats(t *testing.T) {
	// Create fake CIDs for testing
	head1, _ := cid.Parse("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	head2, _ := cid.Parse("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyke")

	// Create a *stats instance with fake values
	fakeStats := &stats{
		heads:      []cid.Cid{head1, head2},
		maxHeight:  10,
		queuedJobs: 5,
	}

	// Test Type method
	assert.Equal(t, kvdb.TypeCRDT, fakeStats.Type())

	// Test Heads method
	assert.Assert(t, slices.CompareFunc(fakeStats.Heads(), []cid.Cid{head1, head2}, func(a, b cid.Cid) int {
		return strings.Compare(a.String(), b.String())
	}) == 0)

	// Test Encode method
	encodedStats := fakeStats.Encode()

	var decodedStats pb.Stats
	err := decodedStats.UnmarshalVT(encodedStats)
	assert.NilError(t, err)

	// Verify the decoded stats
	assert.Equal(t, uint64(10), decodedStats.MaxHeight)
	assert.Equal(t, int64(5), decodedStats.QueuedJobs)

	// Convert decoded Heads from [][]byte to []cid.Cid for comparison
	var decodedHeads []cid.Cid
	for _, headBytes := range decodedStats.Heads {
		c, err := cid.Cast(headBytes)
		assert.NilError(t, err)
		decodedHeads = append(decodedHeads, c)
	}

	// Test Heads method
	assert.Assert(t, slices.CompareFunc(decodedHeads, []cid.Cid{head1, head2}, func(a, b cid.Cid) int {
		return strings.Compare(a.String(), b.String())
	}) == 0)

	// Test Decode method
	newStats := &stats{}
	err = newStats.Decode(encodedStats)
	assert.NilError(t, err)

	// Verify the newStats fields
	assert.Equal(t, fakeStats.maxHeight, newStats.maxHeight)
	assert.Equal(t, fakeStats.queuedJobs, newStats.queuedJobs)
	assert.Assert(t, slices.CompareFunc(newStats.heads, fakeStats.heads, func(a, b cid.Cid) int {
		return strings.Compare(a.String(), b.String())
	}) == 0)
}

func TestStatsDecodeRefusesWhatIsNotStats(t *testing.T) {
	assert.ErrorContains(t, (&stats{}).Decode([]byte{0xff}), "decoding stats")

	notACid, err := (&pb.Stats{Heads: [][]byte{[]byte("nope")}}).MarshalVT()
	assert.NilError(t, err)
	assert.ErrorContains(t, (&stats{}).Decode(notACid), "parsing cid")
}
