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

// Get parse params form args and start download
func Get(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("url is missing")
	}
	url := args[0]
	if !utilgo.IsURL(url, true) {
		return fmt.Errorf("url format error")
	}
	saveas, err := utilgo.GetStorePath(url)
	if err != nil {
		return err
	}
	file, fstart, err := utilgo.GetContinue(saveas)
	if err != nil {
		return err
	}
	defer file.Close()

	var (
		reqHeader                 = util.ParseCookieUaRefer(args)
		mirrors                   = util.GetMirrors(args)
		thread, chunk, start, end = util.ParseThreadchunkStartEnd(args, 8, 2097152, -1, 0)
		transport                 = util.GetTransport(args)
	)
	mirrors[url] = 1
	if start == -1 {
		start = fstart
	}
	return Load(file, mirrors, thread, chunk, start, end, reqHeader, transport, os.Stdout, nil)
}

// Load do high level wrap of fastload for cli usage, it is caller's responsibility to close file
func Load(file *os.File, mirrors map[string]int, thread int32, chunk int64, start int64, end int64, reqHeader http.Header, transport *http.Transport, writer io.Writer, hook func(loaded float64, speed float64, remain float64)) error {
	var (
		logger   *log.Logger
		progress func(received int64, readed int64, total int64, start int64, end int64)
	)
	if writer != nil {
		logger = log.New(writer, "", 0)
		progress = utilgo.ProgressBar(path.Base(file.Name())+" ", "", hook, writer)
	}

	loader := fastload.NewLoader(mirrors, thread, chunk, 4, reqHeader, progress, transport, log.New(os.Stderr, "", 0))
	reader, respHeader, total, filesize, statusCode, err := loader.Load(start, end)
	if logger != nil && os.Getenv("debug") != "" {
		logger.Print(respHeader, statusCode)
	}
	if err != nil {
		if err == io.EOF {
			return errAlready
		}
		return err
	}
	defer reader.Close()
	if logger != nil {
		logger.Print(util.GetWgetInfo(start, end, thread, chunk, total, filesize, file.Name()))
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
