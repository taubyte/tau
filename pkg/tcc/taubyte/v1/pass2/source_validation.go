package pass2

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type sourceValidation struct{}

// SourceValidation returns a transformer that validates function and smartop source
// is either "." (inline) or starts with "libraries/" (library reference).
func SourceValidation() transform.Transformer[object.Refrence] {
	return &sourceValidation{}
}

func (s *sourceValidation) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if err := s.validateSourceGroup(o, "functions", "function"); err != nil {
		return nil, err
	}
	if err := s.validateSourceGroup(o, "smartops", "smartop"); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *sourceValidation) validateSourceGroup(o object.Object[object.Refrence], groupKey, resourceType string) error {
	group, err := o.Child(groupKey).Object()
	if err != nil {
		if err == object.ErrNotExist {
			return nil
		}
		return fmt.Errorf("fetching %s failed with %w", groupKey, err)
	}

	for _, id := range group.Children() {
		sel := group.Child(id)
		source, err := sel.Get("source")
		if err != nil || source == nil {
			continue
		}
		sourceVal, ok := source.(string)
		if !ok {
			return fmt.Errorf("%s %s: source is not a string", resourceType, id)
		}
		if sourceVal != "." && !strings.HasPrefix(sourceVal, "libraries/") {
			return fmt.Errorf("%s %s: source must be \".\" (inline) or start with \"libraries/\", got %q", resourceType, id, sourceVal)
		}
	}
	return nil
}
