package common

// Logger is an interface that defines the methods to log messages.
// but don't emit any log
// If you want to use a nil logger, instead of checking all the time you
// can use a object of this type
type SilentLogger struct{}

// NewSlientLogger creates a new SilentLogger instance.
func NewSlientLogger() Logger {
	return SilentLogger{}
}

func (s SilentLogger) Panicf(format string, args ...interface{}) {}
func (s SilentLogger) Fatalf(format string, args ...interface{}) {}
func (s SilentLogger) Info(args ...interface{})                  {}
func (s SilentLogger) Infof(format string, args ...interface{})  {}
func (s SilentLogger) Error(args ...interface{})                 {}
func (s SilentLogger) Errorf(format string, args ...interface{}) {}
func (s SilentLogger) Warn(args ...interface{})                  {}
func (s SilentLogger) Warnf(format string, args ...interface{})  {}
func (s SilentLogger) Debug(args ...interface{})                 {}
func (s SilentLogger) Debugf(format string, args ...interface{}) {}
