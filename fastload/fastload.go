package fastload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

var (
	rangeReqReg = regexp.MustCompile(`^bytes=(\d+)-(\d+)?$`)
	rangeResReg = regexp.MustCompile(`\d+/(\d+)`)
	bufferPool  = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 262144))
		},
	}
	// ErrCanceled flag this is user canceled
	ErrCanceled = fmt.Errorf("canceled")
)

const (
	reqMethod = "GET"
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
	low        uint8 // 超时系数 (最低允许速度,KB)
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
	ips        []string
	ipchan     chan string
}

type loadertask struct {
	data   *bytes.Buffer
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
				resource.data.Reset()
				bufferPool.Put(resource.data)
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
			f.cancel()
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
			bufferPool.Put(item.data)
		}
	}
	f.dataMap = nil
	return nil
}

//Get load with certain ua and thread thunk
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, start int64, end int64), logger *log.Logger) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	reqHeader := http.Header{}
	return NewLoader(url, 4, 524288, reqHeader, progress, nil, logger).Load(start, end, 64, nil)
}

// NewLoader return new loader instance
func NewLoader(url string, thread int32, thunk int64, reqHeader http.Header, progress func(received int64, readed int64, total int64, start int64, end int64), transport *http.Transport, logger *log.Logger) *Fastloader {
	if logger == nil {
		logger = log.New(ioutil.Discard, "", log.Lshortfile|log.LstdFlags)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Fastloader{
		url:       url,
		thread:    thread,
		thunk:     thunk,
		progress:  progress,
		reqHeader: reqHeader,
		transport: transport,
		logger:    logger,
		tasks:     make(chan *loadertask, thread),
		bytesgot:  make(chan int64, thread*100),
		jobs:      make(chan *loaderjob, thread),
		dataMap:   make(map[int32]*loadertask),

		cancel: cancel,
		ctx:    ctx,
	}
}

// PutIPs 设置IP策略
func (f *Fastloader) PutIPs(ips []string, lock bool) {
	if lock {
		f.ipchan = make(chan string, len(ips))
		for _, ip := range ips {
			f.ipchan <- ip
		}
	} else {
		f.ips = ips
	}
}

//Load return reader , resp , rangesize , total , thread , error ; low should be 16 - 128 bigger means timeout quickly on low speed , when use mirrors , it should be bigger (64-512)
func (f *Fastloader) Load(start int64, end int64, low uint8, mirrors map[string]int) (io.ReadCloser, *http.Response, int64, int64, int32, error) {
	f.low = low
	if mirrors != nil && len(mirrors) > 0 {
		mirrors[f.url] = 1
		f.mirrors = mirrors
		f.mirror = make(chan *mirrorValue, f.thread)
		f.mirrorLock = &sync.Mutex{}
	}
	urlStr := f.bestURL()
	// 如果f.reqHeader 里包含了start,end, 则以reqHeader里的为准
	if rangeStr := f.reqHeader.Get("Range"); rangeStr != "" && rangeReqReg.MatchString(rangeStr) {
		start, end = getReqStartEnd(rangeStr)
	}
	// 发起一个普通请求,验证是否支持断点续传
	var (
		reqHeader = cloneHeader(f.reqHeader)
		timeout   int64
		r         = "Range"
	)
	if end > start && start >= 0 {
		reqHeader.Set(r, fmt.Sprintf("bytes=%d-%d", start, end-1))
		timeout = (end - start) / 1024 / int64(f.low)
	} else if end == 0 && start >= 0 {
		reqHeader.Set(r, fmt.Sprintf("bytes=%d-", start))
		timeout = 3600
	} else {
		return nil, nil, 0, 0, 0, fmt.Errorf("%s:bad range arguements %d-%d ", urlStr, start, end)
	}
	resp, statusOk, err := doRequest(urlStr, reqMethod, reqHeader, timeout, nil, f.transport, "")
	if err != nil {
		return nil, resp, 0, 0, 0, err
	}
	if !statusOk {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return nil, resp, 0, 0, 0, io.EOF
		}
		return nil, resp, 0, 0, 0, fmt.Errorf("%s:status not ok %d", urlStr, resp.StatusCode)
	}
	// 需假设服务器都正确的返回了ContentLength,f.total 必然为正整数
	f.total = resp.ContentLength
	if !resp.ProtoAtLeast(1, 1) {
		return &writeCounter{instance: f, origin: resp.Body}, resp, resp.ContentLength, resp.ContentLength, 1, nil
	}
	f.start = start
	f.filesize = getFileSizeFromResp(resp)
	// 当用户设置的end明显不对,(我们已经获取了length),自动修正这个错误
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
	// 开始派发任务
	go func() {
		var workerNum int32
		for {
			if workerNum < f.thread {
				go f.worker(workerNum)
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
				return
			}
			f.current++
		}
	}()
	return f, resp, f.total, f.filesize, f.thread, nil
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

func getReqStartEnd(rangeStr string) (int64, int64) {
	var (
		start   int64
		end     int64
		matches []string
	)
	matches = rangeReqReg.FindStringSubmatch(rangeStr)
	start, _ = strconv.ParseInt(matches[1], 10, 64)
	if matches[2] != "" {
		end, _ = strconv.ParseInt(matches[2], 10, 64)
	} else {
		end = 0
	}
	return start, end
}

func getFileSizeFromResp(resp *http.Response) int64 {
	var filesize = resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if rangeResReg.MatchString(cr) {
			matches := rangeResReg.FindStringSubmatch(cr)
			filesize, _ = strconv.ParseInt(matches[1], 10, 64)
		}
	}
	return filesize
}

// 三种情况, 1.有锁IP,2无所IP,3.不使用IP
func (f *Fastloader) getIP() string {
	if f.ipchan != nil {
		return <-f.ipchan
	} else if f.ips != nil {
		i := rand.Intn(len(f.ips))
		return f.ips[i]
	}
	return ""
}

// requestItem 必须完成下载一段,会自动调度url
func (f *Fastloader) requestItem(buf *bytes.Buffer, urlStr string, start int64, end int64, ip string) (int64, error) {
	var (
		reqHeader = cloneHeader(f.reqHeader)
		timeout   int64
		body      io.Reader
		limit     int64
		r         = "Range"
	)
	if end > start && start >= 0 {
		reqHeader.Set(r, fmt.Sprintf("bytes=%d-%d", start, end-1))
		timeout = (end - start) / 1024 / int64(f.low)
		limit = end - start
	} else if start == 0 && end == 0 {
		reqHeader.Del(r)
		timeout = 7200
	} else if end == 0 && start > 0 {
		reqHeader.Set(r, fmt.Sprintf("bytes=%d-", start))
		timeout = 3600
	} else {
		return 0, fmt.Errorf("%s:bad range arguements %d-%d ", urlStr, start, end)
	}
	if timeout < 10 {
		timeout = 10
	}
	return doRequestGetBuf(f.ctx, buf, f.mirror, f.bytesgot, urlStr, reqMethod, reqHeader, timeout, body, f.transport, ip, 3, limit)
}

func (f *Fastloader) loadItem(urlStr string, start int64, end int64, ip string) (*bytes.Buffer, error) {
	var (
		buf      = bufferPool.Get().(*bytes.Buffer)
		err      error
		n        int64
		maxtimes = 6
		trytimes = 0
	)
	buf.Reset()
	for {
		n, err = f.requestItem(buf, urlStr, start, end, ip)
		if err == nil {
			return buf, err
		}
		trytimes++
		if trytimes > maxtimes {
			return buf, err
		}
		urlStr = f.bestURL()
		start += n
	}
}

// worker 全部运行在协程内
func (f *Fastloader) worker(no int32) {
	for {
		select {
		case <-f.ctx.Done():
			runtime.Goexit()
		case job, more := <-f.jobs:
			if !more {
				runtime.Goexit()
			}
			// 现在执行的任务与需要等待的任务差距太大,放缓执行
			if job.playno-f.played > 4*f.thread {
				time.Sleep(time.Duration(job.playno-f.played) * time.Millisecond)
			}
			urlStr := f.bestURL()
			ip := f.getIP()
			buf, err := f.loadItem(urlStr, job.start, job.end, ip)
			f.tasks <- &loadertask{data: buf, playno: job.playno, err: err}
			if f.ipchan != nil {
				f.ipchan <- ip
			}
		}
	}
}
