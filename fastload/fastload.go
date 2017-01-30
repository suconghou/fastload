package fastload

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Jobs struct {
	playno uint32
	start  uint64
	end    uint64
}

type Results struct {
	playno  uint32
	start   uint64
	end     uint64
	bits    uint32
	tmpfile *os.File
}

type Stdjob struct {
	start int64
	size  int64
}

func Load(url string, saveas string, start uint64, end uint64, thread uint8, thunk uint32, stdout bool, f func(int, uint64)) {
	if (end <= 0) || (start > end) {
		panic(errors.New("start show not less than end"))
	}
	startTime := time.Now()
	jobs := make(chan Jobs, 8192)
	results := make(chan Results, 8192)
	tasks := make(map[uint32]Results)

	stdjobs := make(chan Stdjob, 128)

	if file, err := os.OpenFile(saveas, os.O_WRONLY|os.O_APPEND, 0666); err == nil {
		totalSize := end - start
		if totalSize < (uint64(thread) * uint64(thunk)) {
			thunk = 262144
			if totalSize < (uint64(thread) * uint64(thunk)) {
				thread = 1
			}
		}
		if stdout {
			go stdoutWorker(saveas, stdjobs)
		}
		var playno uint32 = 0
		for ; playno < uint32(thread); playno++ {
			go worker(url, jobs, results, thunk)
		}
		playno = 0
		for {
			var cstart uint64 = start + uint64(playno*thunk)
			var cend uint64 = cstart + uint64(thunk)
			if cstart >= end {
				break
			}
			if cend >= end {
				cend = end
			}
			jobs <- Jobs{playno: playno, start: cstart, end: cend}
			playno++
		}

		var current uint32 = 0
		var played uint32 = 0

		var downloaded uint64 = 0
		stat, _ := os.Stat(saveas)
		var currpos int64 = stat.Size()
		if currpos > 0 {
			stdjobs <- Stdjob{start: 0, size: currpos}
		}
		for ; current < playno; current++ {
			res := <-results
			tasks[res.playno] = res
			func() {
				for {
					var currentRes Results
					if resource, ok := tasks[played]; ok {
						if resource.tmpfile != nil {
							downloaded = downloaded + uint64(resource.bits)
							bits, err := io.Copy(file, resource.tmpfile)
							if err != nil {
								panic(err)
							}
							resource.tmpfile.Close()
							os.Remove(resource.tmpfile.Name())
							if stdout {
								stdjobs <- Stdjob{start: currpos, size: bits}
							}
							currpos = currpos + bits
						}
						// fmt.Println("Key Found", played)
						played++
						currentRes = resource
					} else {
						// fmt.Println("Key Not Found", played)
						break
					}
					endTime := time.Since(startTime).Seconds()
					speed := float64(downloaded/1024) / endTime
					percent := int((float64(currentRes.end) / float64(end)) * 100)
					leftTime := (float64(end-start)/1024)/speed - endTime
					if !stdout {
						fmt.Printf("\r%s%d%% %s %.2fKB/s %.1fs  %.1fs  %s    ", Bar(percent, 25), percent, ByteFormat(currentRes.end), speed, endTime, leftTime, BoolString(percent > 5, "★", "☆"))
					}
					if f != nil {
						f(percent, downloaded)
					}
				}
			}()
		}
	} else {
		panic(err)
	}

}

func worker(url string, jobs <-chan Jobs, results chan<- Results, thunk uint32) {
	for job := range jobs {
		// fmt.Println(job)
		if tfile, err := ioutil.TempFile("", "disk"+strconv.Itoa(int(job.playno))+"-"); err == nil {
			tmpfile, bits := startChunkLoad(url, tfile, job.start, job.end, job.playno, thunk)
			results <- Results{playno: job.playno, start: job.start, end: job.end, bits: bits, tmpfile: tmpfile}
		} else {
			panic(err)
		}
	}
}

func stdoutWorker(filePath string, stdjobs <-chan Stdjob) {
	if file, err := os.OpenFile(filePath, os.O_RDONLY, 0777); err == nil {
		for job := range stdjobs {
			file.Seek(job.start, 0)
			if _, err = io.CopyN(os.Stdout, file, job.size); err != nil {
				panic(err)
			}
		}
		file.Close()
	}
}

func startChunkLoad(url string, tfile *os.File, start uint64, end uint64, playno uint32, thunk uint32) (*os.File, uint32) {
	client := &http.Client{Timeout: time.Duration(thunk/1024/8) * time.Second}
	if req, err := http.NewRequest("GET", url, nil); err == nil {
		lastLoad := GetContinue(tfile.Name())
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start+lastLoad, end-1))
		if res, err := client.Do(req); err == nil {
			defer res.Body.Close()
			if res.StatusCode == 416 {
				return tfile, 0
			} else if (res.StatusCode >= 200) && (res.StatusCode <= 209) {
				if bits, err := io.Copy(tfile, res.Body); err == nil {
					tfile.Seek(0, 0)
					return tfile, uint32(bits)
				} else {
					os.Stderr.Write([]byte(fmt.Sprintf("\n%s:Error when io copy,Try again\n", err)))
					time.Sleep(time.Second)
					return startChunkLoad(url, tfile, start, end, playno, thunk)
				}
			} else {
				os.Stderr.Write([]byte(fmt.Sprintf("\nDownload error : %s\n", res.Status)))
				panic(errors.New("Download error : " + res.Status))
			}
		} else {
			os.Stderr.Write([]byte(fmt.Sprintf("\n%s:Error when do request,Try again\n", err)))
			time.Sleep(time.Second)
			return startChunkLoad(url, tfile, start, end, playno, thunk)
		}
	} else {
		os.Stderr.Write([]byte(fmt.Sprintf("\n%s:Error when init request", err)))
		panic(err)
	}
}

func CopyToStdOut(filePath string) {
	if file, err := os.OpenFile(filePath, os.O_RDONLY, 0777); err == nil {
		io.Copy(os.Stdout, file)
		file.Close()
	}
}

func GetContinue(saveas string) uint64 {
	var fileSize uint64 = 0
	if stat, err := os.Stat(saveas); os.IsNotExist(err) {
		f, err := os.Create(saveas)
		if err != nil {
			if os.IsPermission(err) {
				os.Stderr.Write([]byte(fmt.Sprintf("%s:GetContinue Error", err)))
				os.Exit(1)
			} else {
				panic(err)
			}
		}
		f.Close()
	} else {
		fileSize = uint64(stat.Size())
	}
	return fileSize
}

func GetStorePath(url string) (string, string) {
	urlName := path.Base(url)
	urlNameArr := strings.Split(urlName, "?")
	urlName = urlNameArr[0]
	dir, _ := os.Getwd()
	filePath := filepath.Join(dir, urlName)
	return urlName, filePath
}

func GetUrlInfo(url string) uint64 {
	var sourceSize uint64 = 0
	response, err := http.Head(url)
	if err != nil {
		os.Stderr.Write([]byte(fmt.Sprintf("Error while get url info %s : %s", url, err)))
		return sourceSize
	}
	if response.StatusCode != http.StatusOK {
		os.Stderr.Write([]byte(fmt.Sprintf("Server return non-200 status: %s\n", response.Status)))
		return sourceSize
	}
	length, _ := strconv.Atoi(response.Header.Get("Content-Length"))
	sourceSize = uint64(length)
	return sourceSize
}

func Bar(vl int, width int) string {
	return fmt.Sprintf("%s %s", strings.Repeat("█", vl/(100/width)), strings.Repeat(" ", width-vl/(100/width)))
}

func BoolString(b bool, s, s1 string) string {
	if b {
		return s
	}
	return s1
}

func ByteFormat(bytes uint64) string {
	unit := [...]string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	if bytes >= 1024 {
		e := math.Floor(math.Log(float64(bytes)) / math.Log(float64(1024)))
		return fmt.Sprintf("%.2f%s", float64(bytes)/math.Pow(1024, math.Floor(e)), unit[int(e)])
	}
	return fmt.Sprintf("%d%s", bytes, unit[0])
}
