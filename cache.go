package freecache

import (
	"sync"
)

type Cache struct {
	locks    [256]sync.Mutex
	segments [256]segment
}

func fnvaHash(data []byte) uint64 {
	var hash uint64 = 14695981039346656037
	for _, c := range data {
		hash ^= uint64(c)
		hash *= 1099511628211
	}
	return hash
}

// The cache size will be set to 512KB at minimum.
func NewCache(size int) (cache *Cache) {
	if size < 512*1024 {
		size = 512 * 1024
	}
	cache = new(Cache)
	for i := 0; i < 256; i++ {
		cache.segments[i] = newSegment(size/256, i)
	}
	return
}

// If the key is larger than 65535 or value is larger than 1/1024 of the cache size,
// the entry will not be written to the cache. expireSeconds <= 0 means no expire,
// but it can be evicted when cache is full.
func (cache *Cache) Set(key, value []byte, expireSeconds int) (err error) {
	hashVal := fnvaHash(key)
	segId := hashVal & 255
	cache.locks[segId].Lock()
	err = cache.segments[segId].set(key, value, hashVal, expireSeconds)
	cache.locks[segId].Unlock()
	return
}

// Get the value or not found error.
func (cache *Cache) Get(key []byte) (value []byte, err error) {
	hashVal := fnvaHash(key)
	segId := hashVal & 255
	cache.locks[segId].Lock()
	value, err = cache.segments[segId].get(key, hashVal)
	cache.locks[segId].Unlock()
	return
}

func (cache *Cache) Del(key []byte) (affected bool) {
	hashVal := fnvaHash(key)
	segId := hashVal & 255
	cache.locks[segId].Lock()
	affected = cache.segments[segId].del(key, hashVal)
	cache.locks[segId].Unlock()
	return
}

func (cache *Cache) EvacuateCount() (count int) {
	for i := 0; i < 256; i++ {
		cache.locks[i].Lock()
		count += cache.segments[i].totalEvacuate
		cache.locks[i].Unlock()
	}
	return
}

func (cache *Cache) EntryCount() (entryCount int64) {
	for i := 0; i < 256; i++ {
		cache.locks[i].Lock()
		entryCount += cache.segments[i].entryCount
		cache.locks[i].Unlock()
	}
	return
}

func (cache *Cache) AverageAccessTime() (averageTime int64) {
	var entryCount, totalTime int64
	for i := 0; i < 256; i++ {
		cache.locks[i].Lock()
		totalTime += cache.segments[i].totalTime
		entryCount += cache.segments[i].entryCount
		cache.locks[i].Unlock()
	}
	averageTime = totalTime / entryCount
	return
}
