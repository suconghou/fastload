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
	return atomic.AddUint64(&ops, 1)
}

// GetWgetInfo return wget stat info
func GetWgetInfo(start int64, end int64, thread int32, chunk int64, total int64, filesize int64, fName string) string {
	var (
		startstr    string
		chunkstr    string
		showsizestr string
	)
	if start != 0 || end != 0 {
		startstr = fmt.Sprintf(",%d-%d", start, end)
	}
	chunkstr = fmt.Sprintf(",分块%dKB", chunk/1024)
	if total > 0 && filesize > 0 {
		showsizestr = fmt.Sprintf(",大小%s/%s(%d/%d)", utilgo.ByteFormat(uint64(total)), utilgo.ByteFormat(uint64(filesize)), total, filesize)
	}
	return fmt.Sprintf("%s\n线程%d%s%s%s", fName, thread, chunkstr, showsizestr, startstr)
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

// ParseThreadchunkStartEnd return thread chunk start end
func ParseThreadchunkStartEnd(args []string, thread int32, chunk int64, start int64, end int64) (int32, int64, int64, int64) {
	if value, err := utilgo.GetParam(args, "--thread"); err == nil {
		t, _ := strconv.Atoi(value)
		if t > 0 && t < 100 {
			thread = int32(t)
		}
	}
	if value, err := utilgo.GetParam(args, "--chunk"); err == nil {
		t, _ := strconv.Atoi(value)
		if t > 64 && t < 8192 {
			chunk = int64(t * 1024)
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
	return thread, chunk, start, end
}
