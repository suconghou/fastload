package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/suconghou/fastload/fastload"
	"github.com/suconghou/fastload/fastloader"
	"github.com/suconghou/fastload/util"
	"github.com/suconghou/utilgo"
)

var (
	errArgs = fmt.Errorf("参数错误")
)

func main() {
	if len(os.Args) > 2 {
		err := cli()
		if err != nil {
			util.Log.Print(err)
		}
	} else {
		err := usage()
		if err != nil {
			util.Log.Print(err)
		}
	}
}

func cli() error {
	switch os.Args[1] {
	case "wget":
		return wget()
	case "serve":
		return serve()
	default:
		return usage()
	}
}

func daemon() error {
	return nil
}

func wget() error {
	url := os.Args[2]
	if !utilgo.IsURL(url, true) {
		return errArgs
	}
	saveas, err := utilgo.GetStorePath(url)
	if err != nil {
		return err
	}
	file, fstart, err := utilgo.GetContinue(saveas)
	if err != nil {
		return err
	}
	defer file.Close()
	return fastloader.Load(file, url, fstart, nil, os.Stdout, nil)
}

func serve() error {
	url := os.Args[2]
	if !utilgo.IsURL(url, true) {
		return errArgs
	}
	var (
		port  string
		upath string
		err   error
	)
	if port, err = utilgo.GetParam("--port"); err != nil {
		port = "6060"
	}
	if !utilgo.IsPort(port) {
		return errArgs
	}
	if upath, err = utilgo.GetParam("--path"); err != nil {
		upath = "/"
	}

	var (
		thread, thunk, start, end = util.ParseThreadThunkStartEnd(8, 1048576, 0, 0)
		mirrors                   = util.GetMirrors()
	)

	http.HandleFunc(upath, func(w http.ResponseWriter, r *http.Request) {
		ID := util.Uqid()
		util.Log.Printf("serve for id %x", ID)
		startTime := time.Now()
		n, err := fastServe(w, r, url, thread, thunk, r.Header, start, end, mirrors)
		speed := float64(n/1024) / time.Since(startTime).Seconds()
		util.Log.Printf("id %x transfered %s @ %.2fKB/s %s", ID, utilgo.ByteFormat(uint64(n)), speed, utilgo.BoolString(err == nil, "", utilgo.BoolString(err == io.EOF, "finished", err.Error())))
	})
	util.Log.Printf("Starting up on port %s\nPath regist %s", port, upath)
	return http.ListenAndServe(":"+port, nil)
}

func fastServe(w http.ResponseWriter, r *http.Request, url string, thread int32, thunk int64, reqHeader http.Header, start int64, end int64, mirrors map[string]int) (int64, error) {
	loader := fastload.NewLoader(url, thread, thunk, reqHeader, nil, nil, nil)
	reader, resp, _, _, _, err := loader.Load(start, end, int64(32+len(mirrors)*2), mirrors)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, err
	}
	closeNotifier, ok := w.(http.CloseNotifier)
	if ok {
		closeNotify := closeNotifier.CloseNotify()
		go func() {
			select {
			case <-closeNotify:
				reader.Close()
			}
		}()
	}
	defer reader.Close()
	out := w.Header()
	for key, value := range resp.Header {
		out.Add(key, value[0])
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == "HEAD" {
		return 0, nil
	}
	return io.Copy(w, reader)
}

func usage() error {
	var (
		s1 = "fatload wget http://url"
		s2 = "fatload serve http://url"
	)
	util.Log.Printf("%s\n%s", s1, s2)
	return nil
}
