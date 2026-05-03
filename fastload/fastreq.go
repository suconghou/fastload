package fastload

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

var (
	rangeReqReg = regexp.MustCompile(`^bytes=(\d+)-(\d+)?$`)
	rangeResReg = regexp.MustCompile(`\d+/(\d+)`)
)

// host 指定固定的DNS解析值，值为一个IP或域名，不能包含端口，请求将发送给这个IP地址
func doRequest(ctx context.Context, urlStr string, method string, reqHeader http.Header, timeout int64, body io.Reader, host string) (*http.Response, bool, error) {
	t := time.Second * time.Duration(timeout)
	ctx2, cancel := context.WithTimeout(ctx, t)
	req, err := http.NewRequestWithContext(ctx2, method, urlStr, body)
	if err != nil {
		cancel()
		return nil, false, err
	}
	req.Header = reqHeader
	var (
		resp *http.Response
	)
	if host != "" {
		mapper := &DNSMapper{
			mappings: map[string]string{
				req.URL.Hostname(): host, // 指定IP或者域名
			},
		}
		cli := http.Client{
			Timeout: t,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           mapper.DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			},
		}
		resp, err = cli.Do(req)
	} else {
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		cancel()
		return resp, false, err
	}
	resp.Body = NewOnCloseReadCloser(resp.Body, func() error {
		cancel()
		return nil
	})
	statusOk := resp.StatusCode/100 == 2
	return resp, statusOk, nil
}

func doRequestGet(ctx context.Context, buf *bytes.Buffer, bytesgot chan<- int64, urlStr string, method string, reqHeader http.Header, timeout int64, body io.Reader, ip string, trytimes uint8, limit int64) (int64, error) {
	var (
		resp      *http.Response
		statusOk  bool
		err       error
		times     uint8
		r         io.Reader
		bytesread int64
	)
	for {
		resp, statusOk, err = doRequest(ctx, urlStr, method, reqHeader, timeout, body, ip)
		if err == nil || times > trytimes {
			break
		}
		times++
	}
	if err != nil {
		return bytesread, err
	}
	defer resp.Body.Close()
	if !statusOk {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return bytesread, io.EOF
		}
		return bytesread, fmt.Errorf("%s:status not ok %d", urlStr, resp.StatusCode)
	}
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit)
	} else {
		r = resp.Body
	}
	for {
		select {
		case <-ctx.Done():
			return bytesread, ctx.Err()
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
			return bytesread, nil
		}
		// 其他情况,read出错,超时等,需要重新发起请求,放弃本次请求,由上层重新调度,本次已下载数据可使用
		return bytesread, err
	}

}

// 下面辅助函数

func fixFetchSegment(reqHeader http.Header, start int64, end int64) (int64, int64) {
	if start > 0 || end > 0 {
		return start, end
	}
	if str := reqHeader.Get("Range"); str != "" && rangeReqReg.MatchString(str) {
		matches := rangeReqReg.FindStringSubmatch(str)
		start, _ = strconv.ParseInt(matches[1], 10, 64)
		if matches[2] != "" {
			end, _ = strconv.ParseInt(matches[2], 10, 64)
		} else {
			end = 0
		}
	}
	return start, end
}

// 返回，文件总大小，本次响应大小
func responseLength(resp *http.Response) (int64, int64) {
	var (
		total    = resp.ContentLength
		filesize = resp.ContentLength
	)
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if rangeResReg.MatchString(cr) {
			matches := rangeResReg.FindStringSubmatch(cr)
			filesize, _ = strconv.ParseInt(matches[1], 10, 64)
		}
	}
	if filesize < total {
		filesize = total
	}
	return filesize, total
}
