package dream

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"

	regionv2 "github.com/taubyte/tau/pkg/proto/gen/tau/region/v2"
	"github.com/taubyte/tau/services/cm"
)

// Regions links the region universes it is handed to as the regions of one cloud
// are linked: what one region's cm carries reaches another region's cm. It stands
// in for the deployment's own link, and keeps to the same contract
// (tau/region/v2): an end speaks for the region it was made for, which is how the
// far end knows who is asking, where a production link proves it.
//
// What crosses is copied, as it would be written to a wire, so neither side holds
// the other's message.
type Regions struct {
	mu   sync.Mutex
	at   map[string][]*attached
	next map[string]int
	cut  map[[2]string]bool
}

// NewRegions is a link with no region on it yet.
func NewRegions() *Regions {
	return &Regions{at: map[string][]*attached{}, next: map[string]int{}, cut: map[[2]string]bool{}}
}

// End is region's end of the link, which every node of that region's universe is
// handed.
func (r *Regions) End(region string) cm.Link { return end{r: r, region: region} }

// Cut stops every call between a and b, both ways, as a partition would, until
// Mend.
func (r *Regions) Cut(a, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cut[pair(a, b)] = true
}

// Mend undoes Cut.
func (r *Regions) Mend(a, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cut, pair(a, b))
}

func pair(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

// receiver is the cm a call to region reaches: each attached one in turn, as a
// production link would spread calls over a region's cm nodes.
func (r *Regions) receiver(from, region string) (cm.Receiver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cut[pair(from, region)] {
		return nil, fmt.Errorf("%w: %s is cut off from %s", cm.ErrUnreachable, region, from)
	}
	held := r.at[region]
	if len(held) == 0 {
		return nil, fmt.Errorf("%w: no cm of %s is on the link", cm.ErrUnreachable, region)
	}
	i := r.next[region] % len(held)
	r.next[region] = i + 1
	return held[i].r, nil
}

type attached struct{ r cm.Receiver }

type end struct {
	r      *Regions
	region string
}

func (e end) Region() string { return e.region }

func (e end) Carry(ctx context.Context, region string, c *regionv2.Carried) (*regionv2.Answered, error) {
	to, err := e.r.receiver(e.region, region)
	if err != nil {
		return nil, err
	}
	answered, err := to.Receive(ctx, e.region, proto.Clone(c).(*regionv2.Carried))
	if err != nil {
		return nil, err
	}
	return proto.Clone(answered).(*regionv2.Answered), nil
}

func (e end) Attach(r cm.Receiver) io.Closer {
	a := &attached{r: r}
	e.r.mu.Lock()
	defer e.r.mu.Unlock()
	e.r.at[e.region] = append(e.r.at[e.region], a)
	return detach{end: e, a: a}
}

type detach struct {
	end
	a *attached
}

func (d detach) Close() error {
	d.r.mu.Lock()
	defer d.r.mu.Unlock()
	d.r.at[d.region] = slices.DeleteFunc(d.r.at[d.region], func(held *attached) bool { return held == d.a })
	return nil
}
