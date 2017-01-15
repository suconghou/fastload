package main

import (
	"fastload"
	"fmt"
)

func main() {
	url := "http://ourwill.cn/static/images/banner4.png?hello"

	var thread uint8 = 2
	var thunk uint32 = 102400

	filename, saveas := fastload.GetStorePath(url)
	start := fastload.GetContinue(saveas)
	end := fastload.GetUrlInfo(url)
	fmt.Println(start, end)
	fastload.Load(url, saveas, start, end, thread, thunk)
	fmt.Println(filename, " 下载完毕")
}
