package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/suconghou/fastload/fastload"
	"github.com/suconghou/fastload/fastloader"
	"github.com/suconghou/fastload/util"
	"github.com/suconghou/utilgo"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
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
	time.Sleep(time.Minute)
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

func wget() error {
	if len(os.Args) < 3 {
		return usage()
	}
	return fastloader.Get(os.Args[2:])
}

func serve() error {
	if len(os.Args) < 3 {
		return usage()
	}
	var (
		url  = os.Args[2]
		args = os.Args[3:]
	)
	if !utilgo.IsURL(url, true) {
		return fmt.Errorf("url format error")
	}
	var (
		port  string
		upath string
		err   error
	)
	if port, err = utilgo.GetParam(args, "--port"); err != nil {
		port = "6060"
	}
	if !utilgo.IsPort(port) {
		return fmt.Errorf("port param is error")
	}
	if upath, err = utilgo.GetParam(args, "--path"); err != nil {
		upath = "/"
	}

	var (
		thread, thunk, start, end = util.ParseThreadThunkStartEnd(args, 8, 1048576, 0, 0)
		mirrors                   = util.GetMirrors(args)
		transport                 = util.GetTransport(args)
	)
	mirrors[url] = 1

	http.HandleFunc(upath, func(w http.ResponseWriter, r *http.Request) {
		ID := util.Uqid()
		util.Log.Printf("serve for id %x", ID)
		startTime := time.Now()
		n, err := fastServe(w, r, mirrors, thread, thunk, r.Header, start, end, transport)
		speed := float64(n/1024) / time.Since(startTime).Seconds()
		var stat string
		if err != nil {
			if err == io.EOF {
				stat = "finished"
			} else {
				stat = err.Error()
			}
		}
		util.Log.Printf("id %x transfered %s @ %.2fKB/s %s", ID, utilgo.ByteFormat(uint64(n)), speed, stat)
	})
	util.Log.Printf("Starting up on port %s\nPath regist %s", port, upath)
	return http.ListenAndServe(":"+port, nil)
}

func fastServe(w http.ResponseWriter, r *http.Request, mirrors map[string]int, thread int32, thunk int64, reqHeader http.Header, start int64, end int64, transport *http.Transport) (int64, error) {
	loader := fastload.NewLoader(mirrors, thread, thunk, 4, reqHeader, nil, transport, nil)
	reader, respHeader, _, _, statusCode, err := loader.Load(start, end)
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
	for key, value := range respHeader {
		out.Set(key, value[0])
	}
	w.WriteHeader(statusCode)
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
