package freecache

import (
	"encoding/binary"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash"
)

const (
	MIN_CACHE_SIZE  = 512 * 1024
	SEGMENT_NUMBER  = 256
	SEG_HASH_REGION = 255
)

type Cache struct {
	locks     [SEGMENT_NUMBER]sync.Mutex
	segments  [SEGMENT_NUMBER]segment
	cacheSize int
	hitCount  int64
	missCount int64
}

func hashFunc(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// The cache size will be set to 512KB at minimum.
// If the size is set relatively large, you should call
// `debug.SetGCPercent()`, set it to a much smaller value
// to limit the memory consumption and GC pause time.
func NewCache(size int) (cache *Cache) {
	if size < MIN_CACHE_SIZE {
		size = MIN_CACHE_SIZE
	}
	cache = new(Cache)
	cache.cacheSize = size
	for i := 0; i < SEGMENT_NUMBER; i++ {
		cache.segments[i] = newSegment(size/SEGMENT_NUMBER, i)
	}
	return
}

// If the key is larger than 65535 or value is larger than 1/1024 of the cache size,
// the entry will not be written to the cache. expireSeconds <= 0 means no expire,
// but it can be evicted when cache is full.
func (cache *Cache) Set(key, value []byte, expireSeconds int) (err error) {
	hashVal := hashFunc(key)
	segId := hashVal & SEG_HASH_REGION
	cache.locks[segId].Lock()
	err = cache.segments[segId].set(key, value, hashVal, expireSeconds)
	cache.locks[segId].Unlock()
	return
}

// Get the value or not found error.
func (cache *Cache) Get(key []byte) (value []byte, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & SEG_HASH_REGION
	cache.locks[segId].Lock()
	value, _, err = cache.segments[segId].get(key, hashVal)
	cache.locks[segId].Unlock()
	if err == nil {
		atomic.AddInt64(&cache.hitCount, 1)
	} else {
		atomic.AddInt64(&cache.missCount, 1)
	}
	return
}

// Get the value or not found error.
func (cache *Cache) GetWithExpiration(key []byte) (value []byte, expireAt uint32, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & SEG_HASH_REGION
	cache.locks[segId].Lock()
	value, expireAt, err = cache.segments[segId].get(key, hashVal)
	cache.locks[segId].Unlock()
	if err == nil {
		atomic.AddInt64(&cache.hitCount, 1)
	} else {
		atomic.AddInt64(&cache.missCount, 1)
	}
	return
}

func (cache *Cache) TTL(key []byte) (timeLeft uint32, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & SEG_HASH_REGION
	timeLeft, err = cache.segments[segId].ttl(key, hashVal)
	return
}

func (cache *Cache) Del(key []byte) (affected bool) {
	hashVal := hashFunc(key)
	segId := hashVal & SEG_HASH_REGION
	cache.locks[segId].Lock()
	affected = cache.segments[segId].del(key, hashVal)
	cache.locks[segId].Unlock()
	return
}

func (cache *Cache) SetInt(key int64, value []byte, expireSeconds int) (err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Set(bKey[:], value, expireSeconds)
}

func (cache *Cache) GetInt(key int64) (value []byte, err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Get(bKey[:])
}

func (cache *Cache) GetIntWithExpiration(key int64) (value []byte, expireAt uint32, err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.GetWithExpiration(bKey[:])
}

func (cache *Cache) DelInt(key int64) (affected bool) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Del(bKey[:])
}

func (cache *Cache) EvacuateCount() (count int64) {
	for i := 0; i < SEGMENT_NUMBER; i++ {
		count += atomic.LoadInt64(&cache.segments[i].totalEvacuate)
	}
	return
}

func (cache *Cache) ExpiredCount() (count int64) {
	for i := 0; i < SEGMENT_NUMBER; i++ {
		count += atomic.LoadInt64(&cache.segments[i].totalExpired)
	}
	return
}

func (cache *Cache) EntryCount() (entryCount int64) {
	it := cache.NewIterator()
	for {
		entry := it.Next()
		if entry == nil {
			break
		}
		entryCount = atomic.AddInt64(&entryCount, 1)
	}
	return entryCount
}

// The average unix timestamp when a entry being accessed.
// Entries have greater access time will be evacuated when it
// is about to be overwritten by new value.
func (cache *Cache) AverageAccessTime() int64 {
	var entryCount, totalTime int64
	for i := 0; i < SEGMENT_NUMBER; i++ {
		totalTime += atomic.LoadInt64(&cache.segments[i].totalTime)
		entryCount += atomic.LoadInt64(&cache.segments[i].totalCount)
	}
	if entryCount == 0 {
		return 0
	} else {
		return totalTime / entryCount
	}
}

func (cache *Cache) HitCount() int64 {
	return atomic.LoadInt64(&cache.hitCount)
}

func (cache *Cache) LookupCount() int64 {
	return atomic.LoadInt64(&cache.hitCount) + atomic.LoadInt64(&cache.missCount)
}

func (cache *Cache) HitRate() float64 {
	lookupCount := cache.LookupCount()
	if lookupCount == 0 {
		return 0
	} else {
		return float64(cache.HitCount()) / float64(lookupCount)
	}
}

func (cache *Cache) OverwriteCount() (overwriteCount int64) {
	for i := 0; i < SEGMENT_NUMBER; i++ {
		overwriteCount += atomic.LoadInt64(&cache.segments[i].overwrites)
	}
	return
}

func (cache *Cache) Clear() {
	for i := 0; i < SEGMENT_NUMBER; i++ {
		cache.locks[i].Lock()
		newSeg := newSegment(len(cache.segments[i].rb.data), i)
		cache.segments[i] = newSeg
		cache.locks[i].Unlock()
	}
	atomic.StoreInt64(&cache.hitCount, 0)
	atomic.StoreInt64(&cache.missCount, 0)
}

func (cache *Cache) Resize(newSize int) {
	size := newSize
	if size < MIN_CACHE_SIZE {
		size = MIN_CACHE_SIZE
	}

	for i := 0; i < SEGMENT_NUMBER; i++ {
		cache.locks[i].Lock()
		if size >= cache.cacheSize {
			cache.segments[i].resize(size / SEGMENT_NUMBER)
		} else {
			//discard all data
			newSeg := newSegment(len(cache.segments[i].rb.data), i)
			cache.segments[i] = newSeg
		}
		cache.locks[i].Unlock()
	}
}

func (cache *Cache) ResetStatistics() {
	atomic.StoreInt64(&cache.hitCount, 0)
	atomic.StoreInt64(&cache.missCount, 0)
	for i := 0; i < SEGMENT_NUMBER; i++ {
		cache.locks[i].Lock()
		cache.segments[i].resetStatistics()
		cache.locks[i].Unlock()
	}
}
