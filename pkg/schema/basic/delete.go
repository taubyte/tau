package basic

import "github.com/taubyte/go-seer"

func (r Resource) Delete(attributes ...string) (err error) {
	if len(attributes) == 0 {
		err = r.Root().Delete().Commit()
		if err != nil {
			return r.WrapError("delete resource failed with: %s", err)
		}

		// Clear name
		r.SetName("")

		return r.seer.Sync()
	}

	queries := make([]*seer.Query, len(attributes))
	for idx, attr := range attributes {
		queries[idx] = r.Config().Get(attr).Delete()
	}

	err = r.seer.Batch(queries...).Commit()
	if err != nil {
		return r.WrapError("delete attributes `%v` from config failed with: %s", attributes, err)
	}

	return r.seer.Sync()
}
