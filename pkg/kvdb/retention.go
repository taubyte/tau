package kvdb

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/fleet"
)

// A FleetSource reads the cloud's published fleet document. published false
// with no error is an answer: the cloud has said nothing, and T is the
// default every node reaches the same way. An error is no answer, and T stays
// what was last read.
type FleetSource func(ctx context.Context) (doc []byte, published bool, err error)

// 0 means the default.
func retentionOf(doc []byte, published bool) (time.Duration, error) {
	if !published {
		return 0, nil
	}
	d, err := fleet.Decode(doc)
	if err != nil {
		return 0, err
	}
	return time.Duration(d.Retention), nil
}

// An unreadable document, or a T some open database cannot hold, changes
// nothing, as every node refuses it the same way.
func (f *factory) applyFleet(ctx context.Context, src FleetSource) {
	doc, published, err := src(ctx)
	if err != nil {
		slogger.Debugf("reading the fleet's retention: %s", err)
		return
	}
	t, err := retentionOf(doc, published)
	if err != nil {
		slogger.Errorf("reading the fleet's retention: %s", err)
		return
	}
	if err := f.setRetention(t); err != nil {
		slogger.Errorf("the fleet's retention: %s", err)
	}
}

func (f *factory) followFleet(ctx context.Context, src FleetSource, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.applyFleet(ctx, src)
		}
	}
}

// 0 means the default. Nothing changes unless every open database can hold t.
func (f *factory) setRetention(t time.Duration) error {
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()
	if f.fixed || t == f.retention {
		return nil
	}
	effective := t
	if effective == 0 {
		effective = DefaultOptions().Retention
	}
	for _, db := range f.dbs {
		if err := db.datastore.opts.checkRetention(effective); err != nil {
			return errors.Join(errors.New(db.path), err)
		}
	}
	for _, db := range f.dbs {
		if err := db.datastore.SetRetention(effective); err != nil {
			return err
		}
	}
	f.retention = t
	return nil
}
