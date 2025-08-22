package chidori

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
)

func Format(logger *log.ZapEventLogger, level log.LogLevel, format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	if logger != nil {
		switch level {
		case log.LevelDPanic:
			logger.DPanic(msg)
		case log.LevelDebug:
			logger.Debug(msg)
		case log.LevelError:
			logger.Error(msg)
		case log.LevelFatal:
			logger.Fatal(msg)
		case log.LevelInfo:
			logger.Info(msg)
		case log.LevelPanic:
			logger.Panic(msg)
		case log.LevelWarn:
			logger.Warn(msg)
		}
	}

	return msg
}
