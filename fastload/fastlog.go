package fastload

import (
	"io"
	"log"
	"os"
)

var debuglog bool = false

func debug(args ...interface{}) {
	if debuglog {
		log.Println(args...)
	}
}

func halt(args ...interface{}) {
	if debuglog {
		log.Println(args...)
	}
	os.Exit(1)
}

func SetDebug(debug bool) {
	debuglog = debug
}

func SetOutput(w io.Writer) {
	log.SetOutput(w)
}
