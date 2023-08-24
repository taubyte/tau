package monkey

import (
	"fmt"
	"io"
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

func (m *Monkey) appendErrors(r io.WriteSeeker, errors ...error) {
	if len(errors) > 0 {
		r.Seek(0, io.SeekEnd)
		r.Write([]byte("\nCI/CD Errors:\n\n"))
		for _, err := range errors {
			r.Write([]byte(err.Error() + "\n"))
		}
	}
}

func (m *Monkey) storeLogs(r io.ReadSeeker, errors ...error) (string, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("logs seek start failed with: %w", err)
	}

	cid, err := m.Service.node.AddFile(r)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %w", err)
	}

	return cid, nil
}
