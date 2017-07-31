package fastload

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"
)

var (
	rangexp   = regexp.MustCompile(`^bytes=(\d+)-(\d+)?$`)
	rangefile = regexp.MustCompile(`\d+/(\d+)`)
)

// Fastloader instance
type Fastloader struct {
	url       string
	thunk     int64
	thread    int32
	total     int64 // 要求下载的大小 range
	filesize  int64 // 文件的真实大小 根据206 header 匹配
	loaded    int64
	readed    int64
	start     int64
	end       int64
	reqHeader http.Header
	logger    *log.Logger
	current   int32
	played    int32
	endno     int32
	tasks     chan loadertask
	jobs      chan loaderjob
	dataMap   map[int32]loadertask
	startTime time.Time
	bytesgot  chan int64
	transport *http.Transport
	progress  func(received int64, readed int64, total int64, duration float64, start int64, end int64)
}

type loadertask struct {
	data   bytes.Buffer
	playno int32
	err    error
}

type loaderjob struct {
	playno int32
	start  int64
	end    int64
}

type writeCounter struct {
	instance *Fastloader
	readed   int64
	origin   io.ReadCloser
	lastrun  time.Time
}

func (wc *writeCounter) Read(p []byte) (int, error) {
	n, err := wc.origin.Read(p)
	if err != nil {
		return n, err
	}
	wc.readed += int64(n)
	timenow := time.Now()
	if wc.readed == wc.instance.total || timenow.Sub(wc.lastrun).Seconds() > 1 {
		wc.instance.progress(wc.readed, wc.readed, wc.instance.total, time.Since(wc.instance.startTime).Seconds(), 0, wc.instance.total)
		wc.lastrun = timenow
	}
	return n, err
}

func (wc *writeCounter) Close() error {
	return wc.origin.Close()
}

func (f *Fastloader) Read(p []byte) (int, error) {
	if f.readed == f.total {
		return 0, io.EOF
	}
	if resource, ok := f.dataMap[f.played]; ok {
		delete(f.dataMap, f.played) // free memory
		n, err := resource.data.Read(p)
		f.readed = f.readed + int64(n)
		if n == 0 && resource.err != nil {
			return 0, resource.err
		}
		if resource.data.Len() == 0 {
			if f.played > 0 && f.played == f.endno {
				if f.progress != nil {
					f.progress(f.loaded, f.readed, f.total, time.Since(f.startTime).Seconds(), f.start, f.end)
				}
				f.Close()
				return n, io.EOF
			}
			if resource.err == nil {
				f.played++
			} else {
				f.dataMap[f.played] = loadertask{data: resource.data, playno: resource.playno, err: resource.err}
			}
		} else {
			f.dataMap[f.played] = loadertask{data: resource.data, playno: resource.playno, err: resource.err}
		}
		return n, err
	}
	for {
		task := <-f.tasks
		f.dataMap[task.playno] = task
		if task.err != nil {
			f.logger.Printf("%s : part %d/%d download error size %d %s", f.url, task.playno, f.endno, task.data.Len(), task.err)
		} else {
			f.logger.Printf("%s : part %d/%d download ok size %d", f.url, task.playno, f.endno, task.data.Len())
		}
		if _, ok := f.dataMap[f.played]; ok {
			return 0, nil
		}
		f.logger.Printf("%s : waiting for part %d", f.url, f.played)
	}
}

//Close clean work
func (f *Fastloader) Close() error {
	f.tasks = make(chan loadertask, 64)
	f.bytesgot = make(chan int64, 8)
	f.jobs = make(chan loaderjob, 64)
	f.dataMap = make(map[int32]loadertask)
	return nil
}

func newClient(url string, reqHeader http.Header, extraHeader http.Header, timeout int64, transport *http.Transport) (*http.Response, error) {
	var client *http.Client
	if transport != nil {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second, Transport: transport}
	} else {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second}
	}
	req, err := newRequest(url, reqHeader, extraHeader)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func newRequest(url string, reqHeader http.Header, extraHeader http.Header) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return req, err
	}
	return reqWithHeader(req, reqHeader, extraHeader), nil
}

func reqWithHeader(req *http.Request, reqHeader http.Header, extraHeader http.Header) *http.Request {
	for key, value := range reqHeader {
		for _, item := range value {
			req.Header.Set(key, item)
		}
	}
	for key, value := range extraHeader {
		for _, item := range value {
			req.Header.Set(key, item)
		}
	}
	return req
}

func respOk(resp *http.Response) bool {
	if resp.StatusCode >= 200 && resp.StatusCode <= 209 {
		return true
	}
	return false
}

func respEnd(resp *http.Response) bool {
	if resp.StatusCode == 416 {
		return true
	}
	return false
}

//Get does
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, duration float64, start int64, end int64)) (io.ReadCloser, int64, int64, int32, error) {
	reqHeader := http.Header{}
	reqHeader.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/59.0.3071.115 Safari/537.36")
	return NewLoader(url, 4, 1048576, reqHeader, progress, nil, os.Stderr).Load(start, end)
}

//NewLoader ...
func NewLoader(url string, thread int32, thunk int64, reqHeader http.Header, progress func(received int64, readed int64, total int64, duration float64, start int64, end int64), transport *http.Transport, out io.Writer) *Fastloader {
	if out == nil {
		out = ioutil.Discard
	}
	loader := &Fastloader{
		url:       url,
		thread:    thread,
		thunk:     thunk,
		progress:  progress,
		reqHeader: reqHeader,
		transport: transport,
		logger:    log.New(out, "", log.Lshortfile|log.LstdFlags),
	}
	loader.Close()
	return loader
}

//Load return reader , resp , rangesize , total , thread , error
func (f *Fastloader) Load(start int64, end int64) (io.ReadCloser, int64, int64, int32, error) {
	start, end = f.fixstartend(start, end)
	f.startTime = time.Now()
	resp, err := f.loadItem(start, end)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if resp.ContentLength > 0 {
		f.total = resp.ContentLength
	}
	if f.total > f.thunk && resp.StatusCode == 206 && resp.ProtoAtLeast(1, 1) {
		cr := resp.Header.Get("Content-Range")
		if rangefile.MatchString(cr) {
			matches := rangefile.FindStringSubmatch(cr)
			f.filesize, _ = strconv.ParseInt(matches[1], 10, 64)
		}
		f.start = start
		if end > f.total+f.start || end <= 0 {
			end = f.total + f.start
		}
		f.end = end
		if f.progress != nil {
			go func() {
				lastrun := time.Now()
				for {
					n := <-f.bytesgot
					f.loaded = f.loaded + n
					timenow := time.Now()
					full := f.start+f.loaded >= f.end
					if full || timenow.Sub(lastrun).Seconds() > 1 {
						f.progress(f.loaded, f.readed, f.total, time.Since(f.startTime).Seconds(), f.start, f.end)
						lastrun = timenow
					}
					if full {
						return
					}
				}
			}()
		}
		var playno = f.current
		go func() {
			data, err := f.getItem(resp, f.start, f.start+f.thunk, playno)
			f.tasks <- loadertask{data: data, playno: playno, err: err}
		}()
		f.current++
		go func() {
			for i := int32(0); i < f.thread; i++ {
				go f.worker()
			}
			for {
				curr := int64(f.current)
				cstart := curr*f.thunk + f.start
				cend := cstart + f.thunk
				if cend >= f.end {
					cend = f.end
					f.endno = f.current
				}
				f.jobs <- loaderjob{start: cstart, end: cend, playno: f.current}
				if f.endno > 0 {
					close(f.jobs)
					break
				} else {
					f.current++
				}
			}
		}()
		return f, f.total, f.filesize, f.thread, nil
	}
	return &writeCounter{instance: f, origin: resp.Body}, resp.ContentLength, resp.ContentLength, 1, nil
}

func (f *Fastloader) fixstartend(start int64, end int64) (int64, int64) {
	rangeStr := f.reqHeader.Get("Range")
	if rangexp.MatchString(rangeStr) {
		var (
			fstart int64
			fend   int64
		)
		matches := rangexp.FindStringSubmatch(rangeStr)
		fstart, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			f.logger.Printf("%s: read range header error %s", f.url, err)
			return start, end
		}
		if matches[2] != "" {
			fend, err = strconv.ParseInt(matches[2], 10, 64)
			if err != nil {
				f.logger.Printf("%s: read range header error %s", f.url, err)
				return start, end
			}
			fend = fend + 1
		} else {
			fend = 0
		}
		return fstart, fend
	}
	return start, end
}

func (f *Fastloader) loadItem(start int64, end int64) (*http.Response, error) {
	var extraHeader = http.Header{}
	var timeout int64
	if end > start && start >= 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
		timeout = (end - start) / 1024 / 4
	} else if start == 0 && end == 0 {
		extraHeader.Set("Range", "bytes=0-")
		timeout = 86400
	} else if end == 0 && start > 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-", start))
		timeout = 3600
	} else {
		return nil, fmt.Errorf("%s : bad range arguements %d-%d ", f.url, start, end)
	}
	if timeout < 60 {
		timeout = 60
	}
	resp, err := newClient(f.url, f.reqHeader, extraHeader, timeout, f.transport)
	if err != nil {
		return resp, err
	}
	if respOk(resp) {
		return resp, nil
	} else if respEnd(resp) {
		return resp, io.EOF
	} else {
		return resp, fmt.Errorf("%s : %s", f.url, resp.Status)
	}
}

func (f *Fastloader) getItem(resp *http.Response, start int64, end int64, playno int32) (bytes.Buffer, error) {
	var (
		data      bytes.Buffer
		trytimes  uint8
		maxtimes  uint8 = 5
		rangesize       = end - start
		cstart    int64
		errmsg    string
	)
	defer resp.Body.Close()
	f.logger.Printf("%s : part %d http range size %d", f.url, playno, rangesize)
	buf := make([]byte, 262144)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if playno == 0 && int64(data.Len()+n) >= rangesize {
				n = int(rangesize) - data.Len()
				data.Write(buf[0:n])
				if f.progress != nil {
					f.bytesgot <- int64(n)
				}
				return data, nil
			}
			data.Write(buf[0:n])
			if f.progress != nil {
				f.bytesgot <- int64(n)
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			return data, nil
		}
		// some error happened
		trytimes = trytimes + 1
		if err, ok := err.(net.Error); ok && err.Timeout() {
			errmsg = fmt.Sprintf("%s : part %d timeout error after %d times %s", f.url, playno, trytimes, err)
		} else if err == io.ErrUnexpectedEOF {
			errmsg = fmt.Sprintf("%s : part %d server closed error after %d times %s", f.url, playno, trytimes, err)
		} else {
			errmsg = fmt.Sprintf("%s : part %d unknow error after %d times %s", f.url, playno, trytimes, err)
		}
		f.logger.Printf(errmsg)
		if trytimes > maxtimes {
			return data, err
		}
		time.Sleep(time.Second)
		cstart = start + int64(data.Len())
		resp, err = f.loadItem(cstart, end)
		if err != nil {
			return data, err
		}
	}
}

func (f *Fastloader) worker() {
	for {
		job, more := <-f.jobs
		if more {
			resp, err := f.loadItem(job.start, job.end)
			if err != nil {
				f.tasks <- loadertask{err: err, playno: job.playno}
			} else {
				data, err := f.getItem(resp, job.start, job.end, job.playno)
				f.tasks <- loadertask{data: data, playno: job.playno, err: err}
			}
		} else {
			return
		}
	}
}
