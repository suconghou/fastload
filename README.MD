## fastload


```

var thread uint8 = 2
var thunk uint32 = 102400
fastload.Load(url, saveas, start, end, thread, thunk, stdout, func(per int, download uint64) {
})
```

for unknow file size set  sen to 0
