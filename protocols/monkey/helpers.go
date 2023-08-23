package monkey

import (
	"fmt"
	"io"
	"os"
	"reflect"
)

func ToNumber(in interface{}) int {
	i := reflect.ValueOf(in)
	switch i.Kind() {
	case reflect.Int64:
		return int(i.Int())
	case reflect.Uint64:
		return int(i.Uint())
	}
	return 0
}

func (m *Monkey) storeLogs(r *os.File, errors ...error) (string, error) {
	if len(errors) > 0 {
		r.Seek(0, io.SeekEnd)
		r.WriteString("\nMonkey Errors:\n\n")
		for _, err := range errors {
			r.WriteString(err.Error() + "\n")
		}
	}

	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("logs seek start failed with: %w", err)
	}

	cid, err := m.Service.node.AddFile(r)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %w", err)
	}

	return cid, nil
}
