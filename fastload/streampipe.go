package fastload

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Pipe get resp from url and response the the request can also be a http proxy
func Pipe(w http.ResponseWriter, r *http.Request, url string, rewriteHeader func(*http.Header, *http.Header, int) int, timeout int64, transport *http.Transport) (int64, error) {
	resp, _, err := doRequest(url, r.Method, r.Header, timeout, r.Body, transport, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, err
	}
	defer resp.Body.Close()
	out := w.Header()
	if rewriteHeader != nil {
		resp.StatusCode = rewriteHeader(&out, &resp.Header, resp.StatusCode)
	} else {
		for key, value := range resp.Header {
			out.Set(key, value[0])
		}
	}
	w.WriteHeader(resp.StatusCode)
	return io.Copy(w, resp.Body)
}

// FastPipe use fastload for pipe, thread should be 2-8 , chunk should be 262144-1048576 (256KB-1024KB), mirrors should not be nil
func FastPipe(w http.ResponseWriter, r *http.Request, mirrors map[string]int, thread int32, chunk int64, rewriteHeader func(*http.Header, *http.Header, int) int, transport *http.Transport) (int64, error) {
	loader := NewLoader(mirrors, thread, chunk, 4, r.Header, nil, transport, nil)
	defer loader.Close()
	body, respHeader, total, filesize, statusCode, err := loader.Load(0, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, err
	}
	defer body.Close()
	out := w.Header()
	if rewriteHeader != nil {
		statusCode = rewriteHeader(&out, &respHeader, statusCode)
	} else {
		if total == filesize {
			statusCode = http.StatusOK
		}
		for key, value := range respHeader {
			out.Set(key, value[0])
		}
	}
	w.WriteHeader(statusCode)
	return io.Copy(w, body)
}

// HTTPProxy is http only proxy no https
func HTTPProxy(w http.ResponseWriter, r *http.Request) error {
	rwc, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	defer rwc.Close()
	hostPortURL, err := url.Parse(r.RequestURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	address := hostPortURL.Host
	if strings.Index(address, ":") == -1 {
		address = address + ":80"
	}
	remote, err := net.DialTimeout("tcp", address, time.Minute)
	if err != nil {
		return err
	}
	defer remote.Close()
	r.Write(remote)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { io.Copy(remote, buf); wg.Done() }()
	go func() { io.Copy(buf, remote); wg.Done() }()
	wg.Wait()
	return nil
}
