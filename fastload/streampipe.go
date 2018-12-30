package fastload

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Pipe get resp from url and response the the request can also be a http proxy
func Pipe(w http.ResponseWriter, r *http.Request, url string, rewriteHeader func(http.Header, *http.Response) int, timeout int64, transport *http.Transport) (int64, error) {
	resp, _, err := doRequest(url, r.Method, r.Header, timeout, r.Body, transport, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, err
	}
	defer resp.Body.Close()
	out := w.Header()
	for key, value := range resp.Header {
		out.Add(key, value[0])
	}
	if rewriteHeader != nil {
		resp.StatusCode = rewriteHeader(out, resp)
	}
	w.WriteHeader(resp.StatusCode)
	return io.Copy(w, resp.Body)
}

// FastPipe use fastload for pipe, thread should be 2-8 , thunk should be 262144-1048576 (256KB-1024KB), len(mirrors) should no more than thread+1 or nil
func FastPipe(w http.ResponseWriter, r *http.Request, url string, thread int32, thunk int64, mirrors map[string]int, rewriteHeader func(http.Header, *http.Response) int, transport *http.Transport) (int64, error) {
	loader := NewLoader(url, thread, thunk, r.Header, nil, transport, nil)
	defer loader.Close()
	body, resp, total, filesize, _, err := loader.Load(0, 0, 32, mirrors)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, err
	}
	defer body.Close()
	out := w.Header()
	if total == filesize {
		resp.StatusCode = http.StatusOK
	}
	for key, value := range resp.Header {
		out.Add(key, value[0])
	}
	if rewriteHeader != nil {
		resp.StatusCode = rewriteHeader(out, resp)
	}
	w.WriteHeader(resp.StatusCode)
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
	address := hostPortURL.Host
	if strings.Index(address, ":") == -1 {
		address = address + ":80"
	}
	remote, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	r.Write(remote)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { io.Copy(remote, rwc); wg.Done() }()
	_, err = io.Copy(buf, remote)
	wg.Wait()
	return err
}
