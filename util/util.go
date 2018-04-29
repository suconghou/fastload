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
	Log    = log.New(os.Stdout, "", 0)
	urlReg = regexp.MustCompile(`^(?i:https?)://[[:print:]]+$`)
	rfull  = regexp.MustCompile(`^--range:(\d+)-(\d+)$`)
	rhalf  = regexp.MustCompile(`^--range:(\d+)-$`)
	ops    uint64
)

// IsURL strict match
func IsURL(url string) bool {
	return urlReg.MatchString(url)
}

// GetMirrors get mirrors
func GetMirrors() map[string]int {
	found := false
	var mirrors = map[string]int{}
	for _, item := range os.Args {
		if !found {
			if item == "--mirrors" {
				found = true
			}
		} else if IsURL(item) {
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

// LogWgetInfo use log print wget info
func LogWgetInfo(start int64, end int64, thread int32, thunk int64, total int64, filesize int64, fName string) {
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
	Log.Printf("%s\n线程%d%s%s%s", fName, thread, thunkstr, showsizestr, startstr)
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
		reqHeader.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/59.0.3071.115 Safari/537.36")
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
