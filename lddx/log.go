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

func LogInit(noColor, quiet bool) {
	color.NoColor = noColor
	isQuiet = quiet
}

func LogError(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Red(format, args...)
}

func LogWarn(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Yellow(format, args...)
}

func LogInfo(format string, args ...interface{}) {
	if isQuiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Green(format, args...)
}

func LogNote(format string, args ...interface{}) {
	if isQuiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Yellow(format, args...)
}
