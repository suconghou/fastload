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
	"strconv"
	"sync"

	"github.com/suconghou/utilgo/pool"
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
	reqMethod = http.MethodGet
)

// Fastloader instance
type Fastloader struct {
	mirrors    map[string]int
	mirror     chan *mirrorValue
	mirrorLock *sync.Mutex
	thunk      int64
	thread     int32
	reqHeader  http.Header
	transport  *http.Transport
	pool       *pool.GoPool

	logger   *log.Logger
	bytesgot chan int64
	progress func(received int64, readed int64, total int64, start int64, end int64)

	cancel context.CancelFunc
	ctx    context.Context

	taskres chan *taskres
	dataMap map[int32]*taskres

	start int64
	end   int64
	low   uint8 // 超时系数 (最低允许速度,KB)

	total    int64 // 要求下载的大小 根据range,本次任务的content-length
	filesize int64 // 文件的真实大小 根据206 header 匹配

	loaded int64 // 网络中已接收字节数
	readed int64 // 已向下级发送的字节数(写盘)

	played int32 // 向下 read 的游标,从0开始计数
	endno  int32

	ips    []string
	ipchan chan string
}

type taskres struct {
	data   *bytes.Buffer
	playno int32
	err    error
}

type writeCounter struct {
	loader *Fastloader
	readed int64
	r      io.ReadCloser
}

type mirrorValue struct {
	url   string
	value int
}

func (w *writeCounter) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	w.readed += int64(n)
	if w.loader.progress != nil {
		total := w.loader.total
		if total <= 0 {
			// 当获取不到文件总大小时,采用模拟值
			if err == io.EOF {
				total = w.readed
			} else {
				total = w.readed * 2
			}
		}
		w.loader.progress(w.readed, w.readed, total, 0, total)
	}
	return n, err
}

func (w *writeCounter) Close() error {
	return w.r.Close()
}

func (f *Fastloader) Read(p []byte) (int, error) {
	if f.readed == f.total {
		return 0, io.EOF
	}
	if res, ok := f.dataMap[f.played]; ok {
		if res.data != nil && res.data.Len() > 0 {
			n, err := res.data.Read(p)
			f.readed += int64(n)
			if res.data.Len() == 0 {
				res.data.Reset()
				bufferPool.Put(res.data)
				if f.played > 0 && f.played == f.endno && res.err == nil {
					// 本次读取的是最后一块数据,并且都读取完了,块也没有错误,最后更新一下进度条
					if f.progress != nil {
						f.progress(f.loaded, f.readed, f.total, f.start, f.end)
					}
					delete(f.dataMap, f.played)
					return n, io.EOF
				}
			}
			// 本次读取后,还没读完,下次继续读取就行了
			return n, err
		} else if res.err != nil { // data is nil & has err, request failed, abort
			f.cancel()
			return 0, res.err
		} else { // data is nil & err is nil, continue
			delete(f.dataMap, f.played)
			f.played++
			return 0, nil
		}
	}
	// 没找到想read的块,就在这里等它
	// 如果执行Load了,而不去read它,会确保taskres被塞满暂停新线程下载
	for {
		select {
		case <-f.ctx.Done():
			return 0, ErrCanceled
		case task := <-f.taskres:
			f.dataMap[task.playno] = task
			// 有新的块来了,更新存储桶,如果来的块是要找的块,我们中断,下次调用就会读这个块了
			if task.playno == f.played {
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
func Get(url string, start int64, end int64, progress func(received int64, readed int64, total int64, start int64, end int64), logger *log.Logger) (io.ReadCloser, http.Header, int64, int64, int, error) {
	return NewLoader(map[string]int{url: 1}, 4, 524288, 4, nil, progress, nil, logger).Load(start, end)
}

// NewLoader return new loader instance
func NewLoader(mirrors map[string]int, thread int32, thunk int64, low uint8, reqHeader http.Header, progress func(received int64, readed int64, total int64, start int64, end int64), transport *http.Transport, logger *log.Logger) *Fastloader {
	if reqHeader == nil {
		reqHeader = http.Header{}
	}
	if logger == nil {
		logger = log.New(ioutil.Discard, "", log.Lshortfile|log.LstdFlags)
	}
	if low < 2 {
		low = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Fastloader{
		thread:    thread,
		thunk:     thunk,
		progress:  progress,
		reqHeader: reqHeader,
		transport: transport,
		logger:    logger,
		taskres:   make(chan *taskres, thread),
		bytesgot:  make(chan int64, thread),
		dataMap:   make(map[int32]*taskres),

		cancel:     cancel,
		ctx:        ctx,
		low:        low,
		mirrors:    mirrors,
		mirror:     make(chan *mirrorValue, thread),
		mirrorLock: &sync.Mutex{},

		pool: pool.New(thread, 5),
	}
}

// PutIPs 设置IP策略, 应在Load之前设置,否则可能引起多线程竞态
// 参数二可以配置最优模型或随机模型, ips数组内元素可重复,当数组长度小于线程数时,饥饿时将会随机选择
func (f *Fastloader) PutIPs(ips []string, lock bool) {
	if lock {
		f.ipchan = make(chan string, len(ips)*10)
		for _, ip := range ips {
			f.ipchan <- ip
		}
	} else {
		f.ips = ips
	}
}

//Load return reader , resp , rangesize , total , thread , error ; low should be 16 - 128 bigger means timeout quickly on low speed , when use mirrors , it should be bigger (64-512)
func (f *Fastloader) Load(start int64, end int64) (io.ReadCloser, http.Header, int64, int64, int, error) {
	urlStr := f.bestURL()
	// 如果f.reqHeader 里包含了start,end,且参数未指明start,end, 则以reqHeader里的为准
	start, end = reGetFetchSegment(f.reqHeader, start, end)
	// 发起一个普通请求,验证是否支持断点续传
	const r = "Range"
	var (
		reqHeader = cloneHeader(f.reqHeader)
		timeout   int64
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
		return nil, nil, 0, 0, 0, err
	}
	if !statusOk {
		resp.Body.Close()
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return nil, resp.Header, 0, 0, resp.StatusCode, io.EOF
		}
		return nil, resp.Header, 0, 0, resp.StatusCode, fmt.Errorf("%s:status not ok %d", urlStr, resp.StatusCode)
	}
	// 需假设服务器都正确的返回了ContentLength,f.total 必然为正整数
	f.start = start
	// filesize 和 total 可能为-1,服务器未指明大小,此时回退到单线程
	f.filesize, f.total = getSizeFromResp(resp)
	if !resp.ProtoAtLeast(1, 1) || (f.filesize == -1 && f.total == -1) {
		return &writeCounter{loader: f, r: resp.Body}, resp.Header, f.total, f.filesize, resp.StatusCode, nil
	}
	resp.Body.Close()
	// 当用户设置的end明显不对,(我们已经获取了length),自动修正这个错误
	if end > f.total+f.start || end <= 0 {
		end = f.total + f.start
	}
	f.end = end

	// 进度条 和 镜像统计
	go func() {
		for {
			select {
			case <-f.ctx.Done():
				return
			case n := <-f.bytesgot:
				f.loaded += n
				if f.progress != nil {
					f.progress(f.loaded, f.readed, f.total, f.start, f.end)
				}
				if f.start+f.loaded >= f.end {
					return
				}
			case v := <-f.mirror:
				f.mirrorLock.Lock()
				f.mirrors[v.url] += v.value
				f.mirrorLock.Unlock()
			}
		}
	}()
	// 开始派发任务
	go func() {
		var curr int32
		for {
			start := int64(curr)*f.thunk + f.start
			end := start + f.thunk
			if end >= f.end {
				end = f.end
				f.endno = curr
			}
			select {
			case <-f.ctx.Done():
				return
			default:
				func(index int32) {
					f.pool.Put(func() {
						f.doTask(start, end, index)
					})
				}(curr)
			}
			if end >= f.end {
				return
			}
			curr++
		}
	}()
	return f, resp.Header, f.total, f.filesize, resp.StatusCode, nil
}

func (f *Fastloader) bestURL() string {
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

// 三种情况, 1.优选IP,2随机IP,3.不使用IP
func (f *Fastloader) bestIP() string {
	if f.ipchan != nil {
		select {
		case ip := <-f.ipchan:
			return ip
		default:
			return f.ips[rand.Intn(len(f.ips))]
		}
	} else if f.ips != nil {
		i := rand.Intn(len(f.ips))
		return f.ips[i]
	}
	return ""
}

// requestItem 由loadItem 多次调度,切换IP和URL,必须完成下载一段
func (f *Fastloader) requestItem(buf *bytes.Buffer, urlStr string, start int64, end int64, ip string) (int64, error) {
	const r = "Range"
	var (
		reqHeader = cloneHeader(f.reqHeader)
		timeout   int64
		body      io.Reader
		limit     int64
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
		return 0, fmt.Errorf("%s:bad range arguements %d-%d", urlStr, start, end)
	}
	if timeout < 10 {
		timeout = 10
	}
	return doRequestGetBuf(f.ctx, buf, f.bytesgot, urlStr, reqMethod, reqHeader, timeout, body, f.transport, ip, 3, limit)
}

// loadItem 调度,确保start,end段被顺利下载,若无法下载,整个任务即中断,期间统计mirror质量,ip质量
func (f *Fastloader) loadItem(start int64, end int64) (*bytes.Buffer, error) {
	var (
		urlStr   = f.bestURL()
		ip       = f.bestIP()
		buf      = bufferPool.Get().(*bytes.Buffer)
		err      error
		n        int64
		maxtimes = 3
		trytimes = 0
	)
	buf.Reset()
	for {
		// 在此统计 mirrors,ip 质量情况
		n, err = f.requestItem(buf, urlStr, start, end, ip)
		// ipchan 的缓存区必须大于线程数,确保此处不会阻塞
		if f.ipchan != nil {
			f.ipchan <- ip
		}
		if err == nil {
			f.mirror <- &mirrorValue{urlStr, 2}
			return buf, nil
		}
		f.mirror <- &mirrorValue{urlStr, -2}
		trytimes++
		if trytimes > maxtimes {
			return buf, err
		}
		urlStr = f.bestURL()
		ip = f.bestIP()
		start += n
	}
}

// doTask 全部运行在协程内
func (f *Fastloader) doTask(start int64, end int64, playno int32) {
	buf, err := f.loadItem(start, end)
	f.taskres <- &taskres{data: buf, playno: playno, err: err}
}

// 下面辅助函数

func reGetFetchSegment(reqHeader http.Header, start int64, end int64) (int64, int64) {
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

func getSizeFromResp(resp *http.Response) (int64, int64) {
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
