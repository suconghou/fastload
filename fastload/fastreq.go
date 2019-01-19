package fastload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func cloneHeader(requestHeader http.Header) http.Header {
	var reqHeader = http.Header{}
	for key, value := range requestHeader {
		for _, item := range value {
			reqHeader.Add(key, item)
		}
	}
	return reqHeader
}

func request(urlStr string, method string, body io.Reader, reqHeader http.Header, ip string) (*http.Request, error) {
	var (
		err error
		req *http.Request
	)
	if ip != "" {
		var urlInfo *url.URL
		urlInfo, err = url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		host := urlInfo.Hostname()
		urlIP := strings.Replace(urlStr, host, ip, 1)
		req, err = http.NewRequest(method, urlIP, body)
		if err != nil {
			return req, err
		}
		req.Host = host
	} else {
		req, err = http.NewRequest(method, urlStr, body)
	}
	if err != nil {
		return req, err
	}
	req.Header = reqHeader
	return req, nil
}

func client(timeout int64, transport *http.Transport) *http.Client {
	var client *http.Client
	if transport != nil {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second, Transport: transport}
	} else {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second}
	}
	return client
}

func doRequest(urlStr string, method string, reqHeader http.Header, timeout int64, body io.Reader, transport *http.Transport, ip string) (*http.Response, bool, error) {
	req, err := request(urlStr, method, body, reqHeader, ip)
	if err != nil {
		return nil, false, err
	}
	client := client(timeout, transport)
	resp, err := client.Do(req)
	if err != nil {
		return resp, false, err
	}
	statusOk := resp.StatusCode >= http.StatusOK && resp.StatusCode <= http.StatusIMUsed
	return resp, statusOk, nil
}

func doRequestGetBuf(ctx context.Context, buf *bytes.Buffer, mirror chan<- *mirrorValue, bytesgot chan<- int64, urlStr string, method string, reqHeader http.Header, timeout int64, body io.Reader, transport *http.Transport, ip string, trytimes uint8, limit int64) (int64, error) {
	var (
		resp      *http.Response
		statusOk  bool
		err       error
		times     uint8
		r         io.Reader
		bytesread int64
	)
	for {
		resp, statusOk, err = doRequest(urlStr, method, reqHeader, timeout, body, transport, ip)
		if err == nil || times > trytimes {
			break
		}
		times++
		time.Sleep(time.Millisecond)
	}
	if !statusOk {
		if err != nil {
			return bytesread, err
		} else if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return bytesread, io.EOF
		}
		return bytesread, fmt.Errorf("%s:status not ok %d", urlStr, resp.StatusCode)
	}
	defer resp.Body.Close()
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit)
	} else {
		r = resp.Body
	}
	for {
		select {
		case <-ctx.Done():
			return bytesread, ErrCanceled
		default:
		}
		n, err := io.CopyN(buf, r, 8192)
		if n > 0 {
			bytesread += n
			if bytesgot != nil {
				bytesgot <- n
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			// 下载完毕
			if mirror != nil {
				mirror <- &mirrorValue{urlStr, 2}
			}
			return bytesread, nil
		}
		// 其他情况,read出错,超时等,需要重新发起请求,放弃本次请求,由上层重新调度,本次已下载数据可使用
		if mirror != nil {
			mirror <- &mirrorValue{urlStr, -2}
		}
		return bytesread, err
	}

}
