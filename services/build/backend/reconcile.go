package backend

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// reconcile walks the open rows for a job whose event was written and whose queue
// item was not (a crash between the two writes), then expires what finished long
// ago. Re-queueing is safe by queue.Push's own dedup; the age gate keeps the pass
// from a Push and barrier per open job.
func (s *server) reconcile() error {
	if err := s.lead(); err != nil {
		return err
	}

	cutoff := s.cfg.Queue.VisibilityTimeout + s.cfg.BuildTimeout
	now := s.now()

	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	keys, err := s.records.List(ctx, openRoot())
	if err != nil {
		return errUnavailable("listing the open rows: %v", err)
	}
	// One mend per job, however many openings it has rows for.
	mended := map[string]bool{}
	for _, key := range keys {
		// BOTH halves, not just the id: mend deletes this job's rows, and an
		// entry whose key it was merely handed cannot say what it deletes.
		repositoryKey, id, err := parseOpenRow(key)
		if err != nil {
			return errInternal("%v", err)
		}
		if mended[repositoryKey+"\x00"+id] {
			continue
		}
		mended[repositoryKey+"\x00"+id] = true
		if err := s.mend(repositoryKey, id, now, cutoff); err != nil {
			return err
		}
	}
	return s.expire(now)
}

// expire erases the jobs that finished longer than JobRetention ago, oldest first,
// keeping those whose build a deployment still runs.
func (s *server) expire(now time.Time) error {
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	keys, err := s.records.List(ctx, terminalRows())
	if err != nil {
		return errUnavailable("listing the retention rows: %v", err)
	}
	slices.Sort(keys)
	cutoff := now.Add(-s.cfg.JobRetention).Unix()
	for _, key := range keys {
		unix, id, err := parseTerminalRow(key)
		if err != nil {
			return errInternal("%v", err)
		}
		if unix >= cutoff {
			return nil
		}
		if err := s.expireJob(id, unix); err != nil {
			return err
		}
	}
	return nil
}

// expireJob is one expired job, one section: the job the row names, if it still ended
// at the second the row says and nothing it built still runs.
func (s *server) expireJob(id string, retained int64) error {
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	return s.withJob(id, func(j *held) error {
		rec := j.rec()
		if !j.found() || !rec.terminal() || rec.UpdatedAt != retained {
			return j.writes().dropStaleRetained(retained).commit("dropping the stale retention row of")
		}
		for asset := range rec.Assets {
			environment, resource, ok := splitAssetKey(asset)
			if !ok {
				continue
			}
			_, from, ok, err := s.journal.live(ctx, rec.ProjectID, environment, resource)
			if err != nil {
				return err
			}
			if ok && from == id {
				return nil
			}
		}
		return j.writes().erase().commit("erasing")
	})
}

// mend is one job of the walk, one section per job so the pass never stalls Cancel
// and claims. An ended job loses the open rows of the events its read saw, and no
// others, so a reopening this replica has not seen keeps its row (AGENTS.md rule
// 5); a job with no events loses the rows older than the cutoff. Neither needs a
// barrier: the rows say which decision wrote them. The push needs none either:
// Push dedups, and a stale item is dropped by begin.
func (s *server) mend(repositoryKey, id string, now time.Time, cutoff time.Duration) error {
	return s.withJobUnbarriered(id, func(j *held) error {
		var drop *entry
		switch {
		case !j.found():
			drop = j.writes().dropOrphanedOpen(repositoryKey, now.Add(-cutoff))
		case j.rec().terminal():
			drop = j.writes().dropClosedOpen(repositoryKey)
		case now.Sub(time.Unix(j.rec().UpdatedAt, 0)) < cutoff:
			return nil
		default:
			if err := j.push(); err != nil {
				return fmt.Errorf("re-queueing job %s: %w", id, err)
			}
			return nil
		}
		if drop.empty() {
			return nil
		}
		return drop.commit("dropping the stale open rows of")
	})
}
