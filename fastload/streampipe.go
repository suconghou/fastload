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
