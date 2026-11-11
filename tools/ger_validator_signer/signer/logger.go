package signer

import (
	"fmt"
	"log/slog"
	"os"
)

// LogAdapter lets go_signer log with slog: it implements the Logger interface of go_signer
type LogAdapter struct {
	logger *slog.Logger
}

// NewLogAdapter creates the adapter
func NewLogAdapter(logger *slog.Logger) *LogAdapter {
	return &LogAdapter{logger: logger.With("module", "signer")}
}

// Fatalf logs the message and stops the process
func (l *LogAdapter) Fatalf(format string, args ...any) {
	l.logger.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

// Info logs at info level
func (l *LogAdapter) Info(args ...any) { l.logger.Info(fmt.Sprint(args...)) }

// Infof logs at info level
func (l *LogAdapter) Infof(format string, args ...any) { l.logger.Info(fmt.Sprintf(format, args...)) }

// Error logs at error level
func (l *LogAdapter) Error(args ...any) { l.logger.Error(fmt.Sprint(args...)) }

// Errorf logs at error level
func (l *LogAdapter) Errorf(format string, args ...any) { l.logger.Error(fmt.Sprintf(format, args...)) }

// Warn logs at warn level
func (l *LogAdapter) Warn(args ...any) { l.logger.Warn(fmt.Sprint(args...)) }

// Warnf logs at warn level
func (l *LogAdapter) Warnf(format string, args ...any) { l.logger.Warn(fmt.Sprintf(format, args...)) }

// Debug logs at debug level
func (l *LogAdapter) Debug(args ...any) { l.logger.Debug(fmt.Sprint(args...)) }

// Debugf logs at debug level
func (l *LogAdapter) Debugf(format string, args ...any) { l.logger.Debug(fmt.Sprintf(format, args...)) }
