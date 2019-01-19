package util

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"

	"github.com/suconghou/utilgo"
)

var (
	// Log to stdout
	Log   = log.New(os.Stdout, "", 0)
	rfull = regexp.MustCompile(`^--range:(\d+)-(\d+)$`)
	rhalf = regexp.MustCompile(`^--range:(\d+)-$`)
	ops   uint64
)

// GetMirrors get mirrors
func GetMirrors() map[string]int {
	found := false
	var mirrors = map[string]int{}
	for _, item := range os.Args {
		if !found {
			if item == "--mirrors" {
				found = true
			}
		} else if utilgo.IsURL(item, true) {
			mirrors[item] = 1
		}
	}
	if len(mirrors) > 0 {
		return mirrors
	}
	return nil
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
	if thread > 1 {
		thunkstr = fmt.Sprintf(",分块%dKB", thunk/1024)
	}
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
func ParseCookieUaRefer() http.Header {
	reqHeader := http.Header{}
	if value, err := utilgo.GetParam("--cookie"); err == nil {
		reqHeader.Add("Cookie", value)
	}
	if value, err := utilgo.GetParam("--ua"); err == nil {
		reqHeader.Add("User-Agent", value)
	} else {
		reqHeader.Add("User-Agent", "curl/7.54.0")
	}
	if value, err := utilgo.GetParam("--refer"); err == nil {
		reqHeader.Add("Referer", value)
	}
	return reqHeader
}

// ParseThreadThunkStartEnd return thread thunk start end
func ParseThreadThunkStartEnd(thread int32, thunk int64, start int64, end int64) (int32, int64, int64, int64) {
	if utilgo.HasFlag("--most") {
		thread = thread * 4
	} else if utilgo.HasFlag("--fast") {
		thread = thread * 2
	} else if utilgo.HasFlag("--slow") {
		thread = thread / 2
	} else if utilgo.HasFlag("--single") {
		thread = 1
	}
	if utilgo.HasFlag("--thin") {
		thunk = thunk / 8
	} else if utilgo.HasFlag("--small") {
		thunk = thunk / 4
	} else if utilgo.HasFlag("--slim") {
		thunk = thunk / 2
	} else if utilgo.HasFlag("--fat") {
		thunk = thunk * 4
	}
	for _, item := range os.Args {
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
