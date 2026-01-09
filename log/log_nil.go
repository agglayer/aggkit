package log

import (
	golog "log"
)

type LoggerNil struct{}

func NewLoggerNil() *LoggerNil {
	return &LoggerNil{}
}

// Debug calls log.Debug
func (l *LoggerNil) Debug(args ...interface{}) {}

// Info calls log.Info
func (l *LoggerNil) Info(args ...interface{}) {}

// Warn calls log.Warn
func (l *LoggerNil) Warn(args ...interface{}) {}

// Error calls log.Error
func (l *LoggerNil) Error(args ...interface{}) {}

// Fatal calls log.Fatal
func (l *LoggerNil) Fatal(args ...interface{}) {
	golog.Fatal(args...)
}

// Debugf calls log.Debugf
func (l *LoggerNil) Debugf(template string, args ...interface{}) {}

// Infof calls log.Infof
func (l *LoggerNil) Infof(template string, args ...interface{}) {}

// Warnf calls log.Warnf
func (l *LoggerNil) Warnf(template string, args ...interface{}) {}

// Fatalf calls log.Fatalf
func (l *LoggerNil) Fatalf(template string, args ...interface{}) {
	golog.Fatalf(template, args...)
}

// Panicf calls log.Panicf
func (l *LoggerNil) Panicf(template string, args ...interface{}) {
	golog.Panicf(template, args...)
}

// Errorf calls log.Errorf
func (l *LoggerNil) Errorf(template string, args ...interface{}) {}
