package monkey

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/ipfs/go-log/v2"
	chidori "github.com/taubyte/utils/logger/zap"
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

func (m *Monkey) addDebugMsg(level log.LogLevel, format string, args ...any) {
	msg := chidori.Format(logger, level, format, args...)
	m.debug += msg + "\n"
}

func (m *Monkey) storeLogs(r *os.File) (string, error) {
	if _, err := r.Seek(0, io.SeekEnd); err == nil {
		r.WriteString("DEBUG: \n" + m.debug + "\n")
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
