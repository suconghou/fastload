package fastload

import (
	"fmt"
	"io"
	"net/http"
)

// Pipe get resp from url and response the the request
func Pipe(w http.ResponseWriter, r *http.Request, url string, rewriteHeader func(http.ResponseWriter, http.Header)) (int64, error) {
	resp, err := newClient(url, r.Method, r.Header, nil, 3600, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), 500)
		return 0, err
	}
	for key, value := range resp.Header {
		w.Header().Add(key, value[0])
	}
	w.WriteHeader(resp.StatusCode)
	if rewriteHeader != nil {
		rewriteHeader(w, resp.Header)
	}
	defer resp.Body.Close()
	return io.Copy(w, resp.Body)
}
