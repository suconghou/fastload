package fastload

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"
)

// Fastloader instance
type Fastloader struct {
	url       string
	thunk     int64
	thread    int32
	total     int64
	loaded    int64
	readed    int64
	start     int64
	end       int64
	reqHeader http.Header
	debug     bool
	current   int32
	played    int32
	endno     int32
	tasks     chan loadertask
	jobs      chan loaderjob
	dataMap   map[int32]loadertask
	startTime time.Time
	bytesgot  chan int64
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
		runtime.GC()
		debug.FreeOSMemory()
		return n, err
	}
	for {
		task := <-f.tasks
		f.dataMap[task.playno] = task
		if task.err != nil {
			f.log(fmt.Sprintf("%s : part %d/%d download error size %d %s", f.url, task.playno, f.endno, task.data.Len(), task.err))
		} else {
			f.log(fmt.Sprintf("%s : part %d/%d download ok size %d", f.url, task.playno, f.endno, task.data.Len()))
		}
		if _, ok := f.dataMap[f.played]; ok {
			return 0, nil
		}
		f.log(fmt.Sprintf("%s : waiting for part %d", f.url, f.played))
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

func (f *Fastloader) log(args ...interface{}) {
	if f.debug {
		log.Println(args...)
	}
}

func newClient(url string, reqHeader http.Header, extraHeader http.Header, timeout int64) (*http.Response, error) {
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
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
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, duration float64, start int64, end int64)) (io.ReadCloser, int64, bool, error) {
	return NewLoader(url, 2, 1048576, progress, false).Load(start, end)
}

//NewLoader ...
func NewLoader(url string, thread int32, thunk int64, progress func(received int64, readed int64, total int64, duration float64, start int64, end int64), debug bool) *Fastloader {
	loader := &Fastloader{
		url:      url,
		thread:   thread,
		thunk:    thunk,
		progress: progress,
		debug:    debug,
	}
	loader.Close()
	return loader
}

//Load return reader
func (f *Fastloader) Load(start int64, end int64) (io.ReadCloser, int64, bool, error) {
	f.startTime = time.Now()
	resp, err := f.loadItem(start, end)
	if err != nil {
		return nil, -1, false, err
	}
	if resp.ContentLength > 0 {
		f.total = resp.ContentLength
	}
	if f.total > f.thunk && resp.StatusCode == 206 && resp.ProtoAtLeast(1, 1) {
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
		return f, f.total, true, nil
	}
	return resp.Body, resp.ContentLength, false, nil
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
	resp, err := newClient(f.url, f.reqHeader, extraHeader, timeout)
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
		bufsize   = 524288
		oncesize  = 262144
		trytimes  uint8
		maxtimes  uint8 = 5
		r         *bufio.Reader
		rangesize = end - start
		cstart    int64
	)
	if rangesize > 0 {
		r = bufio.NewReaderSize(io.LimitReader(resp.Body, rangesize), bufsize)
	} else {
		r = bufio.NewReaderSize(resp.Body, bufsize)
	}
	buf := make([]byte, oncesize)
	for {
		n, err := io.ReadFull(r, buf)
		bytesgot := int64(n)
		if err == nil {
			data.Write(buf)
			if f.progress != nil {
				f.bytesgot <- bytesgot
			}
			f.log(fmt.Sprintf("%s : part %d http received %d bytes full", f.url, playno, n))
		} else if err == io.EOF {
			resp.Body.Close()
			return data, nil
		} else if err == io.ErrUnexpectedEOF && n > 0 {
			data.Write(buf[0:n])
			if f.progress != nil {
				f.bytesgot <- bytesgot
			}
			f.log(fmt.Sprintf("%s : part %d http received %d bytes not full", f.url, playno, n))
		} else {
			resp.Body.Close()
			var msg string
			trytimes = trytimes + 1
			if err, ok := err.(net.Error); ok && err.Timeout() {
				msg = fmt.Sprintf("%s : part %d timeout error after %d times %s", f.url, playno, trytimes, err)
			} else if err == io.ErrUnexpectedEOF && n == 0 {
				msg = fmt.Sprintf("%s : part %d server closed error after %d times %s", f.url, playno, trytimes, err)
			} else {
				msg = fmt.Sprintf("%s : part %d unknow error after %d times %s", f.url, playno, trytimes, err)
			}
			f.log(msg)
			if trytimes > maxtimes {
				return data, err
			}
			time.Sleep(time.Second)
			cstart = start + int64(data.Len())
			resp, err = f.loadItem(cstart, end)
			if err != nil {
				return data, err
			}
			rangesize = end - cstart
			if rangesize > 0 {
				r = bufio.NewReaderSize(io.LimitReader(resp.Body, rangesize), bufsize)
			} else {
				r = bufio.NewReaderSize(resp.Body, bufsize)
			}
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
