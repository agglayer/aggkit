// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// Logger is an autogenerated mock type for the Logger type
type Logger struct {
	mock.Mock
}

type Logger_Expecter struct {
	mock *mock.Mock
}

func (_m *Logger) EXPECT() *Logger_Expecter {
	return &Logger_Expecter{mock: &_m.Mock}
}

// Debug provides a mock function with given fields: args
func (_m *Logger) Debug(args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Debug_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Debug'
type Logger_Debug_Call struct {
	*mock.Call
}

// Debug is a helper method to define mock.On call
//   - args ...interface{}
func (_e *Logger_Expecter) Debug(args ...interface{}) *Logger_Debug_Call {
	return &Logger_Debug_Call{Call: _e.mock.On("Debug",
		append([]interface{}{}, args...)...)}
}

func (_c *Logger_Debug_Call) Run(run func(args ...interface{})) *Logger_Debug_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-0)
		for i, a := range args[0:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(variadicArgs...)
	})
	return _c
}

func (_c *Logger_Debug_Call) Return() *Logger_Debug_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Debug_Call) RunAndReturn(run func(...interface{})) *Logger_Debug_Call {
	_c.Run(run)
	return _c
}

// Debugf provides a mock function with given fields: format, args
func (_m *Logger) Debugf(format string, args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, format)
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Debugf_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Debugf'
type Logger_Debugf_Call struct {
	*mock.Call
}

// Debugf is a helper method to define mock.On call
//   - format string
//   - args ...interface{}
func (_e *Logger_Expecter) Debugf(format interface{}, args ...interface{}) *Logger_Debugf_Call {
	return &Logger_Debugf_Call{Call: _e.mock.On("Debugf",
		append([]interface{}{format}, args...)...)}
}

func (_c *Logger_Debugf_Call) Run(run func(format string, args ...interface{})) *Logger_Debugf_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(args[0].(string), variadicArgs...)
	})
	return _c
}

func (_c *Logger_Debugf_Call) Return() *Logger_Debugf_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Debugf_Call) RunAndReturn(run func(string, ...interface{})) *Logger_Debugf_Call {
	_c.Run(run)
	return _c
}

// Error provides a mock function with given fields: args
func (_m *Logger) Error(args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Error_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Error'
type Logger_Error_Call struct {
	*mock.Call
}

// Error is a helper method to define mock.On call
//   - args ...interface{}
func (_e *Logger_Expecter) Error(args ...interface{}) *Logger_Error_Call {
	return &Logger_Error_Call{Call: _e.mock.On("Error",
		append([]interface{}{}, args...)...)}
}

func (_c *Logger_Error_Call) Run(run func(args ...interface{})) *Logger_Error_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-0)
		for i, a := range args[0:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(variadicArgs...)
	})
	return _c
}

func (_c *Logger_Error_Call) Return() *Logger_Error_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Error_Call) RunAndReturn(run func(...interface{})) *Logger_Error_Call {
	_c.Run(run)
	return _c
}

// Errorf provides a mock function with given fields: format, args
func (_m *Logger) Errorf(format string, args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, format)
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Errorf_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Errorf'
type Logger_Errorf_Call struct {
	*mock.Call
}

// Errorf is a helper method to define mock.On call
//   - format string
//   - args ...interface{}
func (_e *Logger_Expecter) Errorf(format interface{}, args ...interface{}) *Logger_Errorf_Call {
	return &Logger_Errorf_Call{Call: _e.mock.On("Errorf",
		append([]interface{}{format}, args...)...)}
}

func (_c *Logger_Errorf_Call) Run(run func(format string, args ...interface{})) *Logger_Errorf_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(args[0].(string), variadicArgs...)
	})
	return _c
}

func (_c *Logger_Errorf_Call) Return() *Logger_Errorf_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Errorf_Call) RunAndReturn(run func(string, ...interface{})) *Logger_Errorf_Call {
	_c.Run(run)
	return _c
}

// Fatalf provides a mock function with given fields: format, args
func (_m *Logger) Fatalf(format string, args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, format)
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Fatalf_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Fatalf'
type Logger_Fatalf_Call struct {
	*mock.Call
}

// Fatalf is a helper method to define mock.On call
//   - format string
//   - args ...interface{}
func (_e *Logger_Expecter) Fatalf(format interface{}, args ...interface{}) *Logger_Fatalf_Call {
	return &Logger_Fatalf_Call{Call: _e.mock.On("Fatalf",
		append([]interface{}{format}, args...)...)}
}

func (_c *Logger_Fatalf_Call) Run(run func(format string, args ...interface{})) *Logger_Fatalf_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(args[0].(string), variadicArgs...)
	})
	return _c
}

func (_c *Logger_Fatalf_Call) Return() *Logger_Fatalf_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Fatalf_Call) RunAndReturn(run func(string, ...interface{})) *Logger_Fatalf_Call {
	_c.Run(run)
	return _c
}

// Info provides a mock function with given fields: args
func (_m *Logger) Info(args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Info_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Info'
type Logger_Info_Call struct {
	*mock.Call
}

// Info is a helper method to define mock.On call
//   - args ...interface{}
func (_e *Logger_Expecter) Info(args ...interface{}) *Logger_Info_Call {
	return &Logger_Info_Call{Call: _e.mock.On("Info",
		append([]interface{}{}, args...)...)}
}

func (_c *Logger_Info_Call) Run(run func(args ...interface{})) *Logger_Info_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-0)
		for i, a := range args[0:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(variadicArgs...)
	})
	return _c
}

func (_c *Logger_Info_Call) Return() *Logger_Info_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Info_Call) RunAndReturn(run func(...interface{})) *Logger_Info_Call {
	_c.Run(run)
	return _c
}

// Infof provides a mock function with given fields: format, args
func (_m *Logger) Infof(format string, args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, format)
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Infof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Infof'
type Logger_Infof_Call struct {
	*mock.Call
}

// Infof is a helper method to define mock.On call
//   - format string
//   - args ...interface{}
func (_e *Logger_Expecter) Infof(format interface{}, args ...interface{}) *Logger_Infof_Call {
	return &Logger_Infof_Call{Call: _e.mock.On("Infof",
		append([]interface{}{format}, args...)...)}
}

func (_c *Logger_Infof_Call) Run(run func(format string, args ...interface{})) *Logger_Infof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(args[0].(string), variadicArgs...)
	})
	return _c
}

func (_c *Logger_Infof_Call) Return() *Logger_Infof_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Infof_Call) RunAndReturn(run func(string, ...interface{})) *Logger_Infof_Call {
	_c.Run(run)
	return _c
}

// Warn provides a mock function with given fields: args
func (_m *Logger) Warn(args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Warn_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Warn'
type Logger_Warn_Call struct {
	*mock.Call
}

// Warn is a helper method to define mock.On call
//   - args ...interface{}
func (_e *Logger_Expecter) Warn(args ...interface{}) *Logger_Warn_Call {
	return &Logger_Warn_Call{Call: _e.mock.On("Warn",
		append([]interface{}{}, args...)...)}
}

func (_c *Logger_Warn_Call) Run(run func(args ...interface{})) *Logger_Warn_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-0)
		for i, a := range args[0:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(variadicArgs...)
	})
	return _c
}

func (_c *Logger_Warn_Call) Return() *Logger_Warn_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Warn_Call) RunAndReturn(run func(...interface{})) *Logger_Warn_Call {
	_c.Run(run)
	return _c
}

// Warnf provides a mock function with given fields: format, args
func (_m *Logger) Warnf(format string, args ...interface{}) {
	var _ca []interface{}
	_ca = append(_ca, format)
	_ca = append(_ca, args...)
	_m.Called(_ca...)
}

// Logger_Warnf_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Warnf'
type Logger_Warnf_Call struct {
	*mock.Call
}

// Warnf is a helper method to define mock.On call
//   - format string
//   - args ...interface{}
func (_e *Logger_Expecter) Warnf(format interface{}, args ...interface{}) *Logger_Warnf_Call {
	return &Logger_Warnf_Call{Call: _e.mock.On("Warnf",
		append([]interface{}{format}, args...)...)}
}

func (_c *Logger_Warnf_Call) Run(run func(format string, args ...interface{})) *Logger_Warnf_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			if a != nil {
				variadicArgs[i] = a.(interface{})
			}
		}
		run(args[0].(string), variadicArgs...)
	})
	return _c
}

func (_c *Logger_Warnf_Call) Return() *Logger_Warnf_Call {
	_c.Call.Return()
	return _c
}

func (_c *Logger_Warnf_Call) RunAndReturn(run func(string, ...interface{})) *Logger_Warnf_Call {
	_c.Run(run)
	return _c
}

// NewLogger creates a new instance of Logger. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewLogger(t interface {
	mock.TestingT
	Cleanup(func())
}) *Logger {
	mock := &Logger{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
