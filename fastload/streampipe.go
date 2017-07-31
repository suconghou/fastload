package fastload

import (
	"fmt"
	"io"
	"net/http"
)

// Pipe get resp from url and response the the request
func Pipe(w http.ResponseWriter, r *http.Request, url string, rewriteHeader func(http.Header, *http.Response) int) (int64, error) {
	resp, err := newClient(url, r.Method, r.Header, nil, 3600, nil)
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
