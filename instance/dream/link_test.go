package dream

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	regionv2 "github.com/taubyte/tau/pkg/proto/gen/tau/region/v2"
	"github.com/taubyte/tau/services/cm"
)

// heard is a cm that answers as the region it names, and keeps what it was handed.
type heard struct {
	region string
	from   []string
	got    []*regionv2.Carried
}

func (h *heard) Receive(_ context.Context, from string, c *regionv2.Carried) (*regionv2.Answered, error) {
	h.from = append(h.from, from)
	h.got = append(h.got, c)
	if h.region == "" {
		return nil, errors.New("refused")
	}
	return &regionv2.Answered{Region: h.region}, nil
}

func TestTheLinkCarriesBetweenRegionsAsTheirEnds(t *testing.T) {
	ctx := t.Context()
	link := NewRegions()
	one, two := link.End("region-1"), link.End("region-2")
	require.Equal(t, "region-2", two.Region())

	_, err := two.Carry(ctx, "region-1", &regionv2.Carried{})
	require.ErrorIs(t, err, cm.ErrUnreachable, "no cm of region-1 is on it yet")

	first, second := &heard{region: "region-1"}, &heard{region: "region-1"}
	detach := one.Attach(first)
	one.Attach(second)

	sent := &regionv2.Carried{Id: "a"}
	got, err := two.Carry(ctx, "region-1", sent)
	require.NoError(t, err)
	require.Equal(t, "region-1", got.GetRegion())
	require.Equal(t, []string{"region-2"}, first.from, "the far end hears which region asked from the link")
	require.NotSame(t, sent, first.got[0], "what crosses is a copy")
	_, err = two.Carry(ctx, "region-1", sent)
	require.NoError(t, err)
	require.Len(t, second.got, 1, "calls are spread over a region's cms")

	link.Cut("region-2", "region-1")
	_, err = two.Carry(ctx, "region-1", sent)
	require.ErrorIs(t, err, cm.ErrUnreachable)
	_, err = one.Carry(ctx, "region-2", sent)
	require.ErrorIs(t, err, cm.ErrUnreachable, "both ways")
	link.Mend("region-1", "region-2")

	require.NoError(t, detach.Close())
	for range 3 {
		_, err = two.Carry(ctx, "region-1", sent)
		require.NoError(t, err)
	}
	require.Len(t, first.got, 1, "a detached cm hears nothing more")

	refused := &heard{}
	link.End("region-3").Attach(refused)
	_, err = two.Carry(ctx, "region-3", sent)
	require.ErrorContains(t, err, "refused")
	require.NotErrorIs(t, err, cm.ErrUnreachable, "reached, and refused")
}
