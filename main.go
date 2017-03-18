package main

import (
	"fmt"
	"github.com/suconghou/fastload/fastload"
)

func main() {
	url := "http://ourwill.cn/static/images/banner4.png?hello"

	var thread uint8 = 2
	var thunk uint32 = 102400
	var stdout bool = false

	filename, saveas := fastload.GetStorePath(url)
	start, _ := fastload.GetContinue(saveas)
	end, rangAble, err := fastload.GetUrlInfo(url, false)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(start, end, rangAble)
		fastload.Load(url, saveas, start, end, thread, thunk, stdout, func(per int, download uint64) {
		})
		fmt.Println("\n", filename, " 下载完毕")
	}
}
