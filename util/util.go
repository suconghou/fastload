package util

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"

	"github.com/suconghou/utilgo"
	"golang.org/x/net/proxy"
)

var (
	// Log to stdout
	Log   = log.New(os.Stdout, "", 0)
	rfull = regexp.MustCompile(`^--range:(\d+)-(\d+)$`)
	rhalf = regexp.MustCompile(`^--range:(\d+)-$`)
	ops   uint64
)

// GetMirrors get mirrors
func GetMirrors(args []string) map[string]int {
	found := false
	var mirrors = map[string]int{}
	for _, item := range args {
		if !found {
			if item == "--mirrors" {
				found = true
			}
		} else if utilgo.IsURL(item, true) {
			mirrors[item] = 1
		}
	}
	return mirrors
}

// Uqid retrun counter
func Uqid() uint64 {
	atomic.AddUint64(&ops, 1)
	return atomic.LoadUint64(&ops)
}

// GetWgetInfo return wget stat info
func GetWgetInfo(start int64, end int64, thread int32, thunk int64, total int64, filesize int64, fName string) string {
	var (
		startstr    string
		thunkstr    string
		showsizestr string
	)
	if start != 0 || end != 0 {
		startstr = fmt.Sprintf(",%d-%d", start, end)
	}
	thunkstr = fmt.Sprintf(",分块%dKB", thunk/1024)
	if total > 0 && filesize > 0 {
		showsizestr = fmt.Sprintf(",大小%s/%s(%d/%d)", utilgo.ByteFormat(uint64(total)), utilgo.ByteFormat(uint64(filesize)), total, filesize)
	}
	return fmt.Sprintf("%s\n线程%d%s%s%s", fName, thread, thunkstr, showsizestr, startstr)
}

// GetWgetStat return task end stat info
func GetWgetStat(n int64, total int64) string {
	return fmt.Sprintf("\n下载完毕,%d%s", n, utilgo.BoolString(total > 0, fmt.Sprintf("/%d", total), ""))
}

// ParseCookieUaRefer return http.Header
func ParseCookieUaRefer(args []string) http.Header {
	reqHeader := http.Header{}
	if value, err := utilgo.GetParam(args, "--cookie"); err == nil {
		reqHeader.Add("Cookie", value)
	}
	if value, err := utilgo.GetParam(args, "--ua"); err == nil {
		reqHeader.Add("User-Agent", value)
	} else {
		reqHeader.Add("User-Agent", "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.70 Mobile Safari/537.36")
	}
	if value, err := utilgo.GetParam(args, "--refer"); err == nil {
		reqHeader.Add("Referer", value)
	}
	return reqHeader
}

// ParseThreadThunkStartEnd return thread thunk start end
func ParseThreadThunkStartEnd(args []string, thread int32, thunk int64, start int64, end int64) (int32, int64, int64, int64) {
	if value, err := utilgo.GetParam(args, "--thread"); err == nil {
		t, _ := strconv.Atoi(value)
		if t > 0 && t < 100 {
			thread = int32(t)
		}
	}
	if value, err := utilgo.GetParam(args, "--thunk"); err == nil {
		t, _ := strconv.Atoi(value)
		if t > 64 && t < 8192 {
			thunk = int64(t * 1024)
		}
	}
	for _, item := range args {
		if rfull.MatchString(item) {
			matches := rfull.FindStringSubmatch(item)
			start, _ = strconv.ParseInt(matches[1], 10, 64)
			end, _ = strconv.ParseInt(matches[2], 10, 64)
			break
		} else if rhalf.MatchString(item) {
			matches := rhalf.FindStringSubmatch(item)
			start, _ = strconv.ParseInt(matches[1], 10, 64)
			break
		}
	}
	return thread, thunk, start, end
}

// GetTransport return *http.Transport
func GetTransport(args []string) *http.Transport {
	var (
		skipVerify = utilgo.HasFlag(args, "--no-check-certificate")
		tlsCfg     = &tls.Config{InsecureSkipVerify: skipVerify}
	)
	if str, err := utilgo.GetParam(args, "--socks"); err == nil {
		dialer, err := proxy.SOCKS5("tcp", str, nil, proxy.Direct)
		if err == nil {
			return &http.Transport{Dial: dialer.Dial, TLSClientConfig: tlsCfg}
		}
	} else if str, err := utilgo.GetParam(args, "--proxy"); err == nil {
		urli := url.URL{}
		urlproxy, err := urli.Parse(str)
		if err == nil {
			return &http.Transport{Proxy: http.ProxyURL(urlproxy), TLSClientConfig: tlsCfg}
		}
	}
	return &http.Transport{TLSClientConfig: tlsCfg}
}
