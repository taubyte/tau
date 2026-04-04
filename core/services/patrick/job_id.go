package patrick

import (
	"fmt"

	"github.com/taubyte/tau/utils/id"
)

// PushEventJobID returns a stable job id for a GitHub push webhook payload.
// It uses repository id, ref, after, and repository.pushed_at. When pushed_at
// is missing (zero) or ref/after are empty, it falls back to a random id for
// legacy payloads and backward compatibility.
func PushEventJobID(meta *Meta) string {
	if meta == nil {
		return id.Generate(0)
	}
	if meta.Repository.PushedAt == 0 || meta.After == "" || meta.Ref == "" {
		return id.Generate(meta.Repository.ID)
	}
	key := fmt.Sprintf("v1:github:%d:%s:%s:%d", meta.Repository.ID, meta.Ref, meta.After, meta.Repository.PushedAt)
	return id.GenerateDeterministic(key)
}
