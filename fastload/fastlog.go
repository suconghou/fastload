package fastload

import (
	"io"
	"log"
	"os"
)

var DebugLog bool = false

func debug(args ...interface{}) {
	if DebugLog {
		log.Println(args...)
	}
}

func halt(args ...interface{}) {
	if DebugLog {
		log.Println(args...)
	}
	os.Exit(1)
}

func SetDebug(debug bool) {
	DebugLog = debug
}

func SetOutput(w io.Writer) {
	log.SetOutput(w)
}
