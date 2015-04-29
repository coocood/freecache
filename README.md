#FreeCache - A cache library for Go with ZERO GC overhead.

Long lived objects in memory introduce expensive GC overhead, the GC latency can go up to hundreds of milliseconds with just a few millions of live objects. 
With FreeCache, you can cache unlimited number of objects in memory without increased GC latency. 

[![Build Status](https://travis-ci.org/coocood/freecache.png?branch=master)](https://travis-ci.org/coocood/freecache)

##Features
* Store hundreds of millions of entries
* Zero GC overhead
* High concurrent thread-safe access
* Pure Go implementation
* Expiration support
* Nearly LRU algorithm
* Strictly limited memory usage
* Come with a toy server that supports a few basic Redis commands with pipeline

##Example

    cacheSize := 1024*1024
    cache := freecache.NewCache(cacheSize)
    key := []byte("abc")
    val := []byte("def")
    expire := 60 // expire in 60 seconds
    cache.Set(key, val, expire)
    got, err := cache.Get(key)
    if err != nil {
        fmt.Println(err)
    } else {
        fmt.Println(string(got))
    }
    affected := cache.Del(key)
    fmt.Println("deleted key ", affected)
    fmt.Println("entry count ", cache.EntryCount())
    
##Notice
* Recommended Go version is 1.4.
* Memory is preallocated. 
* If you allocate large amount of memory, you may need to set `debug.SetGCPercent()` 
to a much lower percentage to get a normal GC frequency.

##How it is done
FreeCache avoids GC overhead by reducing the number of pointers.
No matter how many entries stored in it, there are only 512 pointers.

##License
The MIT License

