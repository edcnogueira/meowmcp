package logging

import (
	"fmt"
	"os"

	waLog "go.mau.fi/whatsmeow/util/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(level string) (*zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	encCfg := zap.NewDevelopmentEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(zapcore.NewConsoleEncoder(encCfg), zapcore.Lock(os.Stderr), lvl)
	return zap.New(core), nil
}

func Whatsmeow(logger *zap.Logger, module string) waLog.Logger {
	return waLogger{s: logger.Named(module).Sugar()}
}

type waLogger struct {
	s *zap.SugaredLogger
}

func (l waLogger) Warnf(msg string, args ...any)  { l.s.Warnf(msg, args...) }
func (l waLogger) Errorf(msg string, args ...any) { l.s.Errorf(msg, args...) }
func (l waLogger) Infof(msg string, args ...any)  { l.s.Infof(msg, args...) }
func (l waLogger) Debugf(msg string, args ...any) { l.s.Debugf(msg, args...) }
func (l waLogger) Sub(module string) waLog.Logger {
	return waLogger{s: l.s.Named(module)}
}
