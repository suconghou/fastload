package main

import (
	"io"
	"os"
	"path"
	"runtime"

	"fmt"

	"time"

	"runtime/debug"

	"github.com/suconghou/fastload/fastload"
	"github.com/suconghou/utilgo"
)

func main() {
	if len(os.Args) > 1 {
		url := os.Args[1]
		saveas, err := utilgo.GetStorePath(url)
		if err != nil {
			os.Stderr.WriteString(fmt.Sprintf("%s : %s", url, err))
		}
		wget(url, saveas)
		runtime.GC()
		debug.FreeOSMemory()
		//test()
	} else {
		os.Stderr.WriteString("usage :  " + os.Args[0] + " url ")
	}
}

func wget(url string, saveas string) {
	file, start, err := utilgo.GetContinue(saveas)
	if err != nil {
		os.Stderr.WriteString(fmt.Sprintf("%s : %s", url, err))
	}
	resp, total, thread, err := fastload.Get(url, start, 0, utilgo.ProgressBar(path.Base(file.Name())+" ", ""))
	if err != nil {
		if err == io.EOF {
			os.Stderr.WriteString(fmt.Sprintf("%s : already done", url))
		} else {
			os.Stderr.WriteString(fmt.Sprintf("%s : %s", url, err))
		}
	} else {
		n, err := io.Copy(file, resp)
		if err != nil {
			os.Stderr.WriteString(fmt.Sprintf("%s : %s", url, err))
		} else {
			os.Stdout.WriteString(fmt.Sprintf("\r\n%s : download ok use thread %v size %d/%d \r\n", url, thread, n, total))
		}
		file.Close()
	}
}

func test() {
	for {
		fmt.Println(runtime.NumGoroutine())
		time.Sleep(time.Second)
	}
}
