## fastload

download file fastly and keep the right stream data

with progressbar and user defined logger



### API 

there are two api totally 

`fastload.Get()` for quick start


`fastload.NewLoader()` for full control by yourself


### the quick way




### the hard way




### others

need http1.1 for threadable  but can also work in http1.0



**memory usage**

`thread` should be 2 - 32 , 8 or 12 is a good choose , usually more than 32 thread cannot speedup your download but cause memory waste and high cpu usage

`thunk` is the data size per thread download , 128KB - 4MB is a good choose , this effect the speed a little but too large thunk size will cause high memory usage

