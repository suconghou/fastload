package fastload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

var (
	rangexp    = regexp.MustCompile(`^bytes=(\d+)-(\d+)?$`)
	rangefile  = regexp.MustCompile(`\d+/(\d+)`)
	bufferPool = make(chan *bytes.Buffer, 128)
	// ErrCanceled flag this is user canceled
	ErrCanceled = fmt.Errorf("canceled")
)

const (
	maxtimes  uint8 = 9
	reqMethod       = "GET"
)

// Fastloader instance
type Fastloader struct {
	url        string
	mirrors    map[string]int
	mirror     chan *mirrorValue
	mirrorLock *sync.Mutex
	thunk      int64
	thread     int32
	total      int64 // 要求下载的大小 range
	filesize   int64 // 文件的真实大小 根据206 header 匹配
	loaded     int64
	readed     int64
	start      int64
	end        int64
	low        int64 // 超时系数 (最低允许速度)
	reqHeader  http.Header
	logger     *log.Logger
	current    int32
	played     int32
	endno      int32
	tasks      chan *loadertask
	jobs       chan *loaderjob
	dataMap    map[int32]*loadertask
	bytesgot   chan int64
	cancel     context.CancelFunc
	ctx        context.Context
	transport  *http.Transport
	progress   func(received int64, readed int64, total int64, start int64, end int64)
}

type loadertask struct {
	data   *bytes.Buffer
	playno int32
	err    error
	url    string
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
}

type mirrorValue struct {
	url   string
	value int
}

func (wc *writeCounter) Read(p []byte) (int, error) {
	n, err := wc.origin.Read(p)
	wc.readed += int64(n)
	if wc.instance.progress != nil {
		total := wc.instance.total
		if total == 0 {
			if err == io.EOF {
				total = wc.readed
			} else {
				total = wc.readed * 2
			}
		}
		wc.instance.progress(wc.readed, wc.readed, total, 0, total)
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
		if resource.data != nil && resource.data.Len() > 0 {
			n, err := resource.data.Read(p)
			f.readed = f.readed + int64(n)
			if resource.data.Len() == 0 {
				bufferPool <- resource.data
				if f.played > 0 && f.played == f.endno && resource.err == nil {
					if f.progress != nil {
						f.progress(f.loaded, f.readed, f.total, f.start, f.end)
					}
					delete(f.dataMap, f.played)
					return n, io.EOF
				}
			}
			return n, err
		} else if resource.err != nil { // data is nil & has err, request failed, abort
			return 0, resource.err
		} else { // data is nil & err is nil, continue
			delete(f.dataMap, f.played)
			f.played++
			return 0, nil
		}
	}
	for {
		select {
		case <-f.ctx.Done():
			return 0, ErrCanceled
		case task := <-f.tasks:
			// f.logger.Printf("%s : got part %d , waiting for part %d", task.url, task.playno, f.played)
			f.dataMap[task.playno] = task
			if _, ok := f.dataMap[f.played]; ok {
				return 0, nil
			}
		}
	}
}

//Close clean work
func (f *Fastloader) Close() error {
	f.cancel()
	for _, item := range f.dataMap {
		if item.data != nil {
			item.data.Reset()
			bufferPool <- item.data
		}
	}
	f.dataMap = nil
	return nil
}

//Get load with certain ua and thread thunk
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, start int64, end int64), out io.Writer) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	reqHeader := http.Header{}
	return NewLoader(url, 4, 524288, reqHeader, progress, nil, out).Load(start, end, 64, nil)
}

//NewLoader return fastloader instance
func NewLoader(url string, thread int32, thunk int64, reqHeader http.Header, progress func(received int64, readed int64, total int64, start int64, end int64), transport *http.Transport, out io.Writer) *Fastloader {
	if out == nil {
		out = ioutil.Discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	loader := &Fastloader{
		url:        url,
		thread:     thread,
		thunk:      thunk,
		progress:   progress,
		reqHeader:  reqHeader,
		transport:  transport,
		logger:     log.New(out, "", log.Lshortfile|log.LstdFlags),
		tasks:      make(chan *loadertask, thread),
		bytesgot:   make(chan int64, thread*10),
		jobs:       make(chan *loaderjob, thread),
		dataMap:    make(map[int32]*loadertask),
		mirror:     make(chan *mirrorValue, thread),
		mirrorLock: &sync.Mutex{},
		cancel:     cancel,
		ctx:        ctx,
	}
	return loader
}

//Load return reader , resp , rangesize , total , thread , error ; low should be 16 - 128 bigger means timeout quickly on low speed , when use mirrors , it should be bigger (64-512)
func (f *Fastloader) Load(start int64, end int64, low int64, mirrors map[string]int) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	if low >= 8 && low <= 512 {
		f.low = low
	} else {
		f.low = int64(32 + len(mirrors)*4)
	}
	if mirrors != nil && len(mirrors) > 0 {
		mirrors[f.url] = 1
		f.mirrors = mirrors
	}
	url := f.bestURL()
	start, end = f.fixstartend(start, end)
	resp, err := f.loadItem(url, start, end)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}
	if resp.ContentLength > 0 {
		f.total = resp.ContentLength
	}
	if f.total > f.thunk && resp.ProtoAtLeast(1, 1) {
		if resp.StatusCode == http.StatusPartialContent {
			cr := resp.Header.Get("Content-Range")
			if rangefile.MatchString(cr) {
				matches := rangefile.FindStringSubmatch(cr)
				f.filesize, _ = strconv.ParseInt(matches[1], 10, 64)
			}
		} else {
			f.filesize = f.total
		}
		f.start = start
		if end > f.total+f.start || end <= 0 {
			end = f.total + f.start
		}
		f.end = end
		if f.progress != nil {
			go func() {
				for {
					select {
					case <-f.ctx.Done():
						return
					case n := <-f.bytesgot:
						f.loaded = f.loaded + n
						f.progress(f.loaded, f.readed, f.total, f.start, f.end)
						if f.start+f.loaded >= f.end {
							return
						}
					}
				}
			}()
		}
		if f.mirrors != nil {
			go func() {
				for {
					select {
					case <-f.ctx.Done():
						return
					case v := <-f.mirror:
						f.mirrorLock.Lock()
						f.mirrors[v.url] += v.value
						f.mirrorLock.Unlock()
					}
				}
			}()
		}
		var playno = f.current
		go func() {
			data, err := f.getItem(resp, f.start, f.start+f.thunk, playno, url)
			resp.Body.Close()
			f.tasks <- &loadertask{data: data, playno: playno, err: err, url: url}
		}()
		f.current++
		go func() {
			var workerNum int32
			for {
				if workerNum < f.thread {
					go f.worker()
					workerNum++
				}
				cstart := int64(f.current)*f.thunk + f.start
				cend := cstart + f.thunk
				if cend >= f.end {
					cend = f.end
					f.endno = f.current
				}
				select {
				case <-f.ctx.Done():
					return
				case f.jobs <- &loaderjob{start: cstart, end: cend, playno: f.current}:
				}
				if f.endno > 0 {
					close(f.jobs)
					break
				} else {
					f.current++
				}
			}
		}()
		return f, resp, f.total, f.filesize, f.thread, nil
	}
	return &writeCounter{instance: f, origin: resp.Body}, resp, resp.ContentLength, resp.ContentLength, 1, nil
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
			f.logger.Print(err)
			return start, end
		}
		if matches[2] != "" {
			fend, err = strconv.ParseInt(matches[2], 10, 64)
			if err != nil {
				f.logger.Print(err)
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

func (f *Fastloader) bestURL() string {
	if f.mirrors != nil {
		f.mirrorLock.Lock()
		u := &mirrorValue{}
		for k, v := range f.mirrors {
			if u.url == "" || v > u.value {
				u.url = k
				u.value = v
			}
		}
		f.mirrors[u.url]--
		f.mirrorLock.Unlock()
		return u.url
	}
	return f.url
}

func (f *Fastloader) loadItem(url string, start int64, end int64) (*http.Response, error) {
	var (
		extraHeader = http.Header{}
		timeout     int64
		resp        *http.Response
		err         error
		trytimes    uint8
	)
	for key, value := range f.reqHeader {
		for _, item := range value {
			extraHeader.Add(key, item)
		}
	}
	if end > start && start >= 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
		timeout = (end - start) / 1024 / f.low
	} else if start == 0 && end == 0 {
		extraHeader.Del("Range")
		timeout = 7200
	} else if end == 0 && start > 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-", start))
		timeout = 3600
	} else {
		return nil, fmt.Errorf("%s : bad range arguements %d-%d ", url, start, end)
	}
	if timeout < 10 {
		timeout = 10
	}
	for {
		resp, err = NewClient(url, reqMethod, extraHeader, timeout, nil, f.transport)
		trytimes++
		if err == nil || trytimes > maxtimes {
			break
		}
	}
	if err != nil {
		return resp, err
	}
	if respOk(resp) {
		return resp, nil
	} else if respEnd(resp) {
		return resp, io.EOF
	} else {
		return resp, fmt.Errorf("%s : %s", url, resp.Status)
	}
}

func (f *Fastloader) getItem(resp *http.Response, start int64, end int64, playno int32, url string) (*bytes.Buffer, error) {
	var (
		data      *bytes.Buffer
		trytimes  uint8
		cstart    int64
		errmsg    string
		rangesize = end - start
		r         io.Reader
	)
	select {
	case data = <-bufferPool:
	default:
		data = bytes.NewBuffer(make([]byte, 0, 262144))
	}
	defer resp.Body.Close()
	if playno == 0 {
		r = io.LimitReader(resp.Body, rangesize)
	} else {
		r = resp.Body
	}
	for {
		select {
		case <-f.ctx.Done():
			data.Reset()
			bufferPool <- data
			runtime.Goexit()
		default:
		}
		n, err := io.CopyN(data, r, 65536)
		if n > 0 {
			if f.progress != nil {
				f.bytesgot <- n
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			if f.mirrors != nil {
				f.mirror <- &mirrorValue{url, 2}
			}
			return data, nil
		}
		// some error happened
		trytimes++
		var value int
		var waitValue time.Duration
		if er, ok := err.(net.Error); ok && er.Timeout() {
			errmsg = fmt.Sprintf("%s : part %d timeout error after %d times %s", url, playno, trytimes, err)
			value = -2
			waitValue = 10
		} else if err == io.ErrUnexpectedEOF {
			errmsg = fmt.Sprintf("%s : part %d ErrUnexpectedEOF error after %d times %s", url, playno, trytimes, err)
			value = -4
			waitValue = 100
		} else {
			errmsg = fmt.Sprintf("%s : part %d error after %d times %s", url, playno, trytimes, err)
			value = -4
			waitValue = 200
		}
		f.logger.Printf(errmsg)
		if f.mirrors != nil {
			f.mirror <- &mirrorValue{url, value}
		}
		if trytimes > maxtimes {
			return data, err
		}
		time.Sleep(time.Millisecond * waitValue)
		cstart = start + int64(data.Len())
		url = f.bestURL()
		resp, err = f.loadItem(url, cstart, end)
		if err != nil {
			if f.mirrors != nil {
				f.mirror <- &mirrorValue{url, -4}
			}
			if er, ok := err.(net.Error); ok && er.Timeout() {
				url = f.bestURL()
				resp, err = f.loadItem(url, cstart, end)
				if err != nil {
					if f.mirrors != nil {
						f.mirror <- &mirrorValue{url, -4}
					}
					return data, err
				}
			} else {
				return data, err
			}
		}
		r = resp.Body
	}
}

func (f *Fastloader) worker() {
	var url string
	for {
		select {
		case <-f.ctx.Done():
			runtime.Goexit()
		case job, more := <-f.jobs:
			if more {
				if job.playno-f.played > 4*f.thread {
					time.Sleep(time.Duration(job.playno-f.played) * time.Millisecond)
				}
				url = f.bestURL()
				resp, err := f.loadItem(url, job.start, job.end)
				if err != nil {
					if f.mirrors != nil {
						f.mirror <- &mirrorValue{url, -4}
					}
					if er, ok := err.(net.Error); ok && er.Timeout() {
						url = f.bestURL()
						resp, err = f.loadItem(url, job.start, job.end)
						if err != nil {
							if f.mirrors != nil {
								f.mirror <- &mirrorValue{url, -4}
							}
							f.tasks <- &loadertask{data: nil, playno: job.playno, err: err, url: url}
						} else {
							data, err := f.getItem(resp, job.start, job.end, job.playno, url)
							f.tasks <- &loadertask{data: data, playno: job.playno, err: err, url: url}
						}
					} else {
						f.tasks <- &loadertask{data: nil, playno: job.playno, err: err, url: url}
					}
				} else {
					data, err := f.getItem(resp, job.start, job.end, job.playno, url)
					f.tasks <- &loadertask{data: data, playno: job.playno, err: err, url: url}
				}
			} else {
				runtime.Goexit()
			}
		}
	}
}
