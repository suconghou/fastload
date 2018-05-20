## fastload

download file fastly and keep the right stream data

with progressbar and user defined logger

mirror download 




### API 


`fastload.Get()` for quick start


`fastload.NewLoader()` for full control by yourself


### the quick way

see `fastloader.go` for detail


### cli command 

*wget*
```
fastload wget http://xxx.com/file --mirrors http://host1.com/file http://host2.com/file 
```

--mirrors

--ua

--refer

--cookie 

--range 

--fast 

--slow

--fat

--thin

*serve*

```
fastload serve http://xxx.com/file
```

--port

--path 


`/tmp/1`
```
http://mirror1/file.mp4
http://mirror2/file.mp4
http://mirror3/file.mp4
```

```
fastload serve http://url/file.mp4 --path /hello.mp4 --slim --mirrors $(cat /tmp/1 | xargs)
```

### others

need http1.1 for threadable  but can also work in http1.0



**memory usage**

`thread` should be 2 - 32 , 8 or 12 is a good choose , usually more than 32 thread cannot speedup your download but cause memory waste and high cpu usage

`thunk` is the data size per thread download , 128KB - 4MB is a good choose , this effect the speed a little but too large thunk size will cause high memory usage

