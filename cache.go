package freecache

import (
	"encoding/binary"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash"
)

const (
	// segmentCount represents the number of segments within a freecache instance.
	segmentCount = 256
	// segmentAndOpVal is bitwise AND applied to the hashVal to find the segment id.
	segmentAndOpVal = segmentCount - 1
	minBufSize      = 512 * 1024
)

// Cache is a freecache instance.
type Cache struct {
	locks    [segmentCount]sync.Mutex
	segments [segmentCount]segment
}

func hashFunc(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// NewCache returns a newly initialize cache by size.
// The cache size will be set to 512KB at minimum.
// If the size is set relatively large, you should call
// `debug.SetGCPercent()`, set it to a much smaller value
// to limit the memory consumption and GC pause time.
func NewCache(size int) (cache *Cache) {
	if size < minBufSize {
		size = minBufSize
	}
	cache = new(Cache)
	for i := 0; i < segmentCount; i++ {
		cache.segments[i] = newSegment(size/segmentCount, i)
	}
	return
}

// Set sets a key, value and expiration for a cache entry and stores it in the cache.
// If the key is larger than 65535 or value is larger than 1/1024 of the cache size,
// the entry will not be written to the cache. expireSeconds <= 0 means no expire,
// but it can be evicted when cache is full.
func (cache *Cache) Set(key, value []byte, expireSeconds int) (err error) {
	hashVal := hashFunc(key)
	segID := hashVal & segmentAndOpVal
	cache.locks[segID].Lock()
	err = cache.segments[segID].set(key, value, hashVal, expireSeconds)
	cache.locks[segID].Unlock()
	return
}

// Get returns the value or not found error.
func (cache *Cache) Get(key []byte) (value []byte, err error) {
	value, _, err = cache.GetWithExpiration(key)
	return
}

// GetWithExpiration returns the value with expiration or not found error.
func (cache *Cache) GetWithExpiration(key []byte) (value []byte, expireAt uint32, err error) {
	hashVal := hashFunc(key)
	segID := hashVal & segmentAndOpVal
	cache.locks[segID].Lock()
	value, expireAt, err = cache.segments[segID].get(key, hashVal)
	cache.locks[segID].Unlock()
	return
}

// TTL returns the TTL time left for a given key or a not found error.
func (cache *Cache) TTL(key []byte) (timeLeft uint32, err error) {
	hashVal := hashFunc(key)
	segID := hashVal & segmentAndOpVal
	cache.locks[segID].Lock()
	timeLeft, err = cache.segments[segID].ttl(key, hashVal)
	cache.locks[segID].Unlock()
	return
}

// Del deletes an item in the cache by key and returns true or false if a delete occurred.
func (cache *Cache) Del(key []byte) (affected bool) {
	hashVal := hashFunc(key)
	segID := hashVal & segmentAndOpVal
	cache.locks[segID].Lock()
	affected = cache.segments[segID].del(key, hashVal)
	cache.locks[segID].Unlock()
	return
}

// SetInt stores in integer value in the cache.
func (cache *Cache) SetInt(key int64, value []byte, expireSeconds int) (err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Set(bKey[:], value, expireSeconds)
}

// GetInt returns the value for an integer within the cache or a not found error.
func (cache *Cache) GetInt(key int64) (value []byte, err error) {
	value, _, err = cache.GetIntWithExpiration(key)
	return
}

// GetIntWithExpiration returns the value and expiration or a not found error.
func (cache *Cache) GetIntWithExpiration(key int64) (value []byte, expireAt uint32, err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.GetWithExpiration(bKey[:])
}

// DelInt deletes an item in the cache by int key and returns true or false if a delete occurred.
func (cache *Cache) DelInt(key int64) (affected bool) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Del(bKey[:])
}

// EvacuateCount is a metric indicating the number of times an eviction occurred.
func (cache *Cache) EvacuateCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].totalEvacuate)
	}
	return
}

// ExpiredCount is a metric indicating the number of times an expire occurred.
func (cache *Cache) ExpiredCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].totalExpired)
	}
	return
}

// EntryCount returns the number of items currently in the cache.
func (cache *Cache) EntryCount() (entryCount int64) {
	for i := range cache.segments {
		entryCount += atomic.LoadInt64(&cache.segments[i].entryCount)
	}
	return
}

// AverageAccessTime returns the average unix timestamp when a entry being accessed.
// Entries have greater access time will be evacuated when it
// is about to be overwritten by new value.
func (cache *Cache) AverageAccessTime() int64 {
	var entryCount, totalTime int64
	for i := range cache.segments {
		totalTime += atomic.LoadInt64(&cache.segments[i].totalTime)
		entryCount += atomic.LoadInt64(&cache.segments[i].totalCount)
	}
	if entryCount == 0 {
		return 0
	} else {
		return totalTime / entryCount
	}
}

// HitCount is a metric that returns number of times a key was found in the cache.
func (cache *Cache) HitCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].hitCount)
	}
	return
}

// MissCount is a metric that returns the number of times a miss occurred in the cache.
func (cache *Cache) MissCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].missCount)
	}
	return
}

// LookupCount is a metric that returns the number of times a lookup for a given key occurred.
func (cache *Cache) LookupCount() int64 {
	return cache.HitCount() + cache.MissCount()
}

// HitRate is the ratio of hits over lookups.
func (cache *Cache) HitRate() float64 {
	hitCount, missCount := cache.HitCount(), cache.MissCount()
	lookupCount := hitCount + missCount
	if lookupCount == 0 {
		return 0
	} else {
		return float64(hitCount) / float64(lookupCount)
	}
}

// OverwriteCount indicates the number of times entries have been overriden.
func (cache *Cache) OverwriteCount() (overwriteCount int64) {
	for i := range cache.segments {
		overwriteCount += atomic.LoadInt64(&cache.segments[i].overwrites)
	}
	return
}

// Clear clears the cache.
func (cache *Cache) Clear() {
	for i := range cache.segments {
		cache.locks[i].Lock()
		cache.segments[i].clear()
		cache.locks[i].Unlock()
	}
}

// ResetStatistics refreshes the current state of the statistics.
func (cache *Cache) ResetStatistics() {
	for i := range cache.segments {
		cache.locks[i].Lock()
		cache.segments[i].resetStatistics()
		cache.locks[i].Unlock()
	}
}
