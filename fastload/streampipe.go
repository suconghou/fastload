package fastload

import (
	"fmt"
	"io"
	"net/http"
)

// Pipe get resp from url and response the the request can also be a http proxy
func Pipe(w http.ResponseWriter, r *http.Request, url string, rewriteHeader func(http.Header, *http.Response) int, timeout int64, transport *http.Transport) (int64, error) {
	resp, err := newClient(url, r.Method, r.Header, nil, timeout, r.Body, transport)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), 500)
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
func FastPipe(w http.ResponseWriter, r *http.Request, url string, thread int32, thunk int64, mirrors []string, rewriteHeader func(http.Header, *http.Response) int, transport *http.Transport) (int64, error) {
	body, resp, _, _, _, err := NewLoader(url, thread, thunk, r.Header, nil, transport, nil).Load(0, 0, mirrors)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), 500)
		return 0, err
	}
	out := w.Header()
	for key, value := range resp.Header {
		out.Add(key, value[0])
	}
	if rewriteHeader != nil {
		resp.StatusCode = rewriteHeader(out, resp)
	}
	w.WriteHeader(resp.StatusCode)
	return io.Copy(w, body)
}
