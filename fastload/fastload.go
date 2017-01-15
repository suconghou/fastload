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
	playno uint8
	start  uint64
	end    uint64
}

type Results struct {
	playno  uint8
	start   uint64
	end     uint64
	bits    uint32
	tmpfile *os.File
}

func Load(url string, saveas string, start uint64, end uint64, thread uint8, thunk uint32) {
	if (end <= 0) || (start > end) {
		panic(errors.New("start show not less than end"))
	}
	startTime := time.Now()
	jobs := make(chan Jobs, 100)
	results := make(chan Results, 100)
	tasks := make(map[uint8]Results)

	if file, err := os.OpenFile(saveas, os.O_WRONLY|os.O_APPEND, 0666); err == nil {
		var playno uint8 = 0
		for ; playno < thread; playno++ {
			go worker(url, saveas, jobs, results)
		}
		playno = 0
		for {
			var cstart uint64 = start + uint64(uint32(playno)*thunk)
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

		var current uint8 = 0
		var played uint8 = 0

		var downloaded uint64 = 0

		for ; current < playno; current++ {
			res := <-results
			tasks[res.playno] = res
			func() {
				for {
					var currentRes Results
					if resource, ok := tasks[played]; ok {
						if resource.tmpfile != nil {
							downloaded = downloaded + uint64(resource.bits)
							io.Copy(file, resource.tmpfile)
							resource.tmpfile.Close()
							os.Remove(resource.tmpfile.Name())
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
					fmt.Printf("\r%s%d%% %s %.2fKB/s %.1fs  %.1fs  %s    ", Bar(percent, 25), percent, ByteFormat(downloaded), speed, endTime, leftTime, BoolString(percent > 5, "★", "☆"))
				}
			}()
		}
	} else {
		panic(err)
	}

}

func worker(url string, saveas string, jobs <-chan Jobs, results chan<- Results) {
	for job := range jobs {
		// fmt.Println(job)
		tmpfile, bits := startChunkLoad(url, saveas, job.start, job.end, job.playno)
		results <- Results{playno: job.playno, start: job.start, end: job.end, bits: bits, tmpfile: tmpfile}
	}
}

func startChunkLoad(url string, saveas string, start uint64, end uint64, playno uint8) (*os.File, uint32) {
	client := &http.Client{}
	if req, err := http.NewRequest("GET", url, nil); err == nil {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
		if res, err := client.Do(req); err == nil {
			defer res.Body.Close()
			if res.StatusCode == 416 {
				return nil, 0
			} else if (res.StatusCode >= 200) && (res.StatusCode <= 209) {
				if tmpfile, err := ioutil.TempFile("", "disk"); err == nil {
					// fmt.Println(res, res.StatusCode, http.StatusOK)
					if bits, err := io.Copy(tmpfile, res.Body); err == nil {
						tmpfile.Seek(0, 0)
						return tmpfile, uint32(bits)
					} else {
						fmt.Println(err, "Error when io copy,Try again")
						time.Sleep(time.Second)
						return startChunkLoad(url, saveas, start, end, playno)
					}
				} else {
					fmt.Println("Error when create tmpfile")
					panic(err)
				}
			} else {
				fmt.Println(res.Status, "Try again")
				time.Sleep(time.Second)
				return startChunkLoad(url, saveas, start, end, playno)
			}
		} else {
			fmt.Println("Error when do request Try again")
			panic(err)
		}
	} else {
		fmt.Println("Error when init request")
		panic(err)
	}
}

func GetContinue(saveas string) uint64 {
	var fileSize uint64 = 0
	if stat, err := os.Stat(saveas); os.IsNotExist(err) {
		f, err := os.Create(saveas)
		if err != nil {
			if os.IsPermission(err) {
				fmt.Println(err)
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
		fmt.Println("Error while get url info ", url, ":", err)
		return sourceSize
	}
	if response.StatusCode != http.StatusOK {
		fmt.Println("Server return non-200 status: %v\n", response.Status)
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
