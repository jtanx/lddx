package lddx

import (
	"sync"

	"github.com/fatih/color"
	"github.com/mattn/go-colorable"
)

var logMutex sync.Mutex
var isQuiet bool

func init() {
	color.Output = colorable.NewColorableStderr()
}

// LogInit initialises the logger
func LogInit(noColor, quiet bool) {
	color.NoColor = noColor
	isQuiet = quiet
}

// LogError logs an error message
func LogError(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Red(format, args...)
}

// LogWarn logs a warning message
func LogWarn(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Yellow(format, args...)
}

// LogInfo logs an info message
func LogInfo(format string, args ...interface{}) {
	if isQuiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Green(format, args...)
}

// LogNote logs a note message
func LogNote(format string, args ...interface{}) {
	if isQuiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Magenta(format, args...)
}
