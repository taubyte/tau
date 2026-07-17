package seer

func (b *Batch) Commit() error {
	for _, q := range b.queries {
		if err := q.Commit(); err != nil {
			return err
		}
	}
	return nil
}
