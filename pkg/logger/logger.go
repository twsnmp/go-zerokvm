// Package logger provides conditional debug logging capabilities.
package logger

import "log"

// DebugEnabled controls whether non-error logs are output.
var DebugEnabled bool

// Debugf prints a formatted log message if debugging is enabled.
func Debugf(format string, v ...interface{}) {
	if DebugEnabled {
		log.Printf(format, v...)
	}
}

// Debugln prints a log message if debugging is enabled.
func Debugln(v ...interface{}) {
	if DebugEnabled {
		log.Println(v...)
	}
}
