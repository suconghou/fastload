package fastloader

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"

	"github.com/suconghou/fastload/fastload"
	"github.com/suconghou/fastload/util"
	"github.com/suconghou/utilgo"
)

var (
	errAlready = fmt.Errorf("该文件已经下载完毕")
)

// Load do high level wrap of fastload , it is caller's responsibility to close file
func Load(file *os.File, url string, fstart int64, transport *http.Transport, writer io.Writer, hook func(loaded float64, speed float64, remain float64)) error {
	var (
		logger                    *log.Logger
		progress                  func(received int64, readed int64, total int64, start int64, end int64)
		reqHeader                 = util.ParseCookieUaRefer()
		thread, thunk, start, end = util.ParseThreadThunkStartEnd(8, 2097152, -1, 0)
		mirrors                   = util.GetMirrors()
	)
	if writer != nil {
		logger = log.New(writer, "", 0)
		progress = utilgo.ProgressBar(path.Base(file.Name()), "", hook, writer)
	}

	if start != fstart && start == -1 {
		start = fstart
	}
	loader := fastload.NewLoader(url, thread, thunk, reqHeader, progress, transport, nil)
	reader, _, total, filesize, threadreal, err := loader.Load(start, end, int64(32+len(mirrors)*2), mirrors)
	if err != nil {
		if err == io.EOF {
			return errAlready
		}
		return err
	}
	defer reader.Close()
	if logger != nil {
		logger.Print(util.GetWgetInfo(start, end, threadreal, thunk, total, filesize, file.Name()))
	}
	n, err := io.Copy(file, reader)
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Print(util.GetWgetStat(n, total))
	}
	return nil
}
