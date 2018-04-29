package fastload

import (
	"io"
	"net/http"
	"time"
)

// NewClient is a http client and do a request
func NewClient(url string, method string, reqHeader http.Header, timeout int64, body io.Reader, transport *http.Transport) (*http.Response, error) {
	var client *http.Client
	if transport != nil {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second, Transport: transport}
	} else {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second}
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header = reqHeader
	return client.Do(req)
}

func respOk(resp *http.Response) bool {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode <= http.StatusIMUsed {
		return true
	}
	return false
}

func respEnd(resp *http.Response) bool {
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return true
	}
	return false
}
