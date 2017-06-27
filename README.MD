## fastload




### API 



`fastload.Get()`



`fastload.NewLoader()`



> thunk 大小   128KB ~ 4MB  thunk 过大会占用较多内存.

> thread 大小 2 ~ 32  thread 设置过大并没有提速效果.thread和thunk都设置过大都会加大内存占用,内存占用可能超过100MB


