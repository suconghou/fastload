package fastload

import (
	"io"
	"log"
)

func SetOutput(w io.Writer) {
	log.SetOutput(w)
}
