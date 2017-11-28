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
	"time"
)

var (
	rangexp    = regexp.MustCompile(`^bytes=(\d+)-(\d+)?$`)
	rangefile  = regexp.MustCompile(`\d+/(\d+)`)
	bufferPool = make(chan *bytes.Buffer, 128)
)

const maxtimes uint8 = 9

// Fastloader instance
type Fastloader struct {
	url       string
	mirrors   []string
	mirror    chan string
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
	tasks     chan *loadertask
	jobs      chan *loaderjob
	dataMap   map[int32]*loadertask
	bytesgot  chan int64
	cancel    context.CancelFunc
	ctx       context.Context
	transport *http.Transport
	progress  func(received int64, readed int64, total int64, start int64, end int64)
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

func (wc *writeCounter) Read(p []byte) (int, error) {
	n, err := wc.origin.Read(p)
	wc.readed += int64(n)
	if wc.instance.progress != nil {
		wc.instance.progress(wc.readed, wc.readed, wc.instance.total, 0, wc.instance.total)
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
			return 0, io.ErrUnexpectedEOF
		default:
		}
		task := <-f.tasks
		f.dataMap[task.playno] = task
		if _, ok := f.dataMap[f.played]; ok {
			return 0, nil
		}
		// f.logger.Printf("%s : got part %d , waiting for part %d", task.url, task.playno, f.played)
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

//Get load with certain ua and thread thunk
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, start int64, end int64), out io.Writer) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	reqHeader := http.Header{}
	reqHeader.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/59.0.3071.115 Safari/537.36")
	return NewLoader(url, 4, 524288, reqHeader, progress, nil, out).Load(start, end, nil)
}

//NewLoader return fastloader instance
func NewLoader(url string, thread int32, thunk int64, reqHeader http.Header, progress func(received int64, readed int64, total int64, start int64, end int64), transport *http.Transport, out io.Writer) *Fastloader {
	if out == nil {
		out = ioutil.Discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	loader := &Fastloader{
		url:       url,
		thread:    thread,
		thunk:     thunk,
		progress:  progress,
		reqHeader: reqHeader,
		transport: transport,
		logger:    log.New(out, "", log.Lshortfile|log.LstdFlags),
		tasks:     make(chan *loadertask, thread),
		bytesgot:  make(chan int64, 8),
		jobs:      make(chan *loaderjob, thread),
		dataMap:   make(map[int32]*loadertask),
		mirror:    make(chan string, 2*thread),
		cancel:    cancel,
		ctx:       ctx,
	}
	loader.mirror <- url
	return loader
}

//Load return reader , resp , rangesize , total , thread , error
func (f *Fastloader) Load(start int64, end int64, mirrors []string) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	start, end = f.fixstartend(start, end)
	resp, url, err := f.loadItem(start, end)
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
		if mirrors != nil && len(mirrors) > 0 {
			f.mirrors = mirrors
			func() {
				var i int32
				for {
					for _, u := range mirrors {
						select {
						case <-f.ctx.Done():
							return
						case f.mirror <- u:
						default:
						}
						i++
						if i > f.thread {
							return
						}
					}
				}
			}()
		}
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
					default:
						n := <-f.bytesgot
						f.loaded = f.loaded + n
						f.progress(f.loaded, f.readed, f.total, f.start, f.end)
						if f.start+f.loaded >= f.end {
							return
						}
					}
				}
			}()
		}
		var playno = f.current
		go func() {
			data, err := f.getItem(resp, f.start, f.start+f.thunk, playno, url)
			f.tasks <- &loadertask{data: data, playno: playno, err: err, url: url}
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

func (f *Fastloader) loadItem(start int64, end int64) (*http.Response, string, error) {
	var (
		extraHeader = http.Header{}
		timeout     int64
		low         int64 = 16
		url               = f.url
		resp        *http.Response
		err         error
		trytimes    uint8
	)
	for key, value := range f.reqHeader {
		for _, item := range value {
			extraHeader.Add(key, item)
		}
	}
	if f.mirrors != nil {
		url = <-f.mirror
		low = 32
	}
	if end > start && start >= 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
		timeout = (end - start) / 1024 / low
	} else if start == 0 && end == 0 {
		extraHeader.Del("Range")
		timeout = 7200
	} else if end == 0 && start > 0 {
		extraHeader.Set("Range", fmt.Sprintf("bytes=%d-", start))
		timeout = 3600
	} else {
		return nil, url, fmt.Errorf("%s : bad range arguements %d-%d ", url, start, end)
	}
	if timeout < 30 {
		timeout = 30
	}
	for {
		resp, err = NewClient(url, "GET", extraHeader, timeout, nil, f.transport)
		trytimes++
		if err == nil || trytimes > maxtimes {
			break
		}
	}
	if err != nil {
		return resp, url, err
	}
	if respOk(resp) {
		return resp, url, nil
	} else if respEnd(resp) {
		return resp, url, io.EOF
	} else {
		return resp, url, fmt.Errorf("%s : %s", url, resp.Status)
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
				f.mirror <- url
			}
			return data, nil
		}
		// some error happened
		trytimes++
		var returl = url
		if er, ok := err.(net.Error); ok && er.Timeout() {
			errmsg = fmt.Sprintf("%s : part %d timeout error after %d times %s", url, playno, trytimes, err)
		} else if err == io.ErrUnexpectedEOF {
			errmsg = fmt.Sprintf("%s : part %d server closed error after %d times %s", url, playno, trytimes, err)
			returl = f.url
		} else {
			errmsg = fmt.Sprintf("%s : part %d error after %d times %s", url, playno, trytimes, err)
			returl = f.url
		}
		f.logger.Printf(errmsg)
		if trytimes > maxtimes {
			if f.mirrors != nil {
				f.mirror <- returl
			}
			return data, err
		}
		time.Sleep(time.Second)
		cstart = start + int64(data.Len())
		resp, url, err = f.loadItem(cstart, end)
		if err != nil {
			if f.mirrors != nil {
				f.mirror <- url
			}
			if er, ok := err.(net.Error); ok && er.Timeout() {
				resp, url, err = f.loadItem(cstart, end)
				if err != nil {
					if f.mirrors != nil {
						f.mirror <- url
					}
					return data, err
				}
			} else {
				return data, err
			}
		}
	}
}

func (f *Fastloader) worker() {
	for {
		select {
		case <-f.ctx.Done():
			runtime.Goexit()
		default:
			job, more := <-f.jobs
			if more {
				if job.playno-f.played > 4*f.thread {
					time.Sleep(time.Duration(job.playno-f.played) * time.Second)
				}
				resp, url, err := f.loadItem(job.start, job.end)
				if err != nil {
					if f.mirrors != nil {
						f.mirror <- url
					}
					if er, ok := err.(net.Error); ok && er.Timeout() {
						resp, url, err = f.loadItem(job.start, job.end)
						if err != nil {
							if f.mirrors != nil {
								f.mirror <- url
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
