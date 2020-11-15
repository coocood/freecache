package freecache

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	mrand "math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFreeCache(t *testing.T) {
	cache := NewCache(1024)
	if cache.HitRate() != 0 {
		t.Error("initial hit rate should be zero")
	}
	if cache.AverageAccessTime() != 0 {
		t.Error("initial average access time should be zero")
	}
	key := []byte("abcd")
	val := []byte("efghijkl")
	err := cache.Set(key, val, 0)
	if err != nil {
		t.Error("err should be nil")
	}
	value, err := cache.Get(key)
	if err != nil || !bytes.Equal(value, val) {
		t.Error("value not equal")
	}
	affected := cache.Del(key)
	if !affected {
		t.Error("del should return affected true")
	}
	value, err = cache.Get(key)
	if err != ErrNotFound {
		t.Error("error should be ErrNotFound after being deleted")
	}
	affected = cache.Del(key)
	if affected {
		t.Error("del should not return affected true")
	}

	cache.Clear()
	n := 5000
	for i := 0; i < n; i++ {
		keyStr := fmt.Sprintf("key%v", i)
		valStr := strings.Repeat(keyStr, 10)
		err = cache.Set([]byte(keyStr), []byte(valStr), 0)
		if err != nil {
			t.Error(err)
		}
	}
	time.Sleep(time.Second)
	for i := 1; i < n; i += 2 {
		keyStr := fmt.Sprintf("key%v", i)
		cache.Get([]byte(keyStr))
	}

	for i := 1; i < n; i += 8 {
		keyStr := fmt.Sprintf("key%v", i)
		cache.Del([]byte(keyStr))
	}

	for i := 0; i < n; i += 2 {
		keyStr := fmt.Sprintf("key%v", i)
		valStr := strings.Repeat(keyStr, 10)
		err = cache.Set([]byte(keyStr), []byte(valStr), 0)
		if err != nil {
			t.Error(err)
		}
	}
	for i := 1; i < n; i += 2 {
		keyStr := fmt.Sprintf("key%v", i)
		expectedValStr := strings.Repeat(keyStr, 10)
		value, err = cache.Get([]byte(keyStr))
		if err == nil {
			if string(value) != expectedValStr {
				t.Errorf("value is %v, expected %v", string(value), expectedValStr)
			}
		}
		err = cache.GetFn([]byte(keyStr), func(val []byte) error {
			if string(val) != expectedValStr {
				t.Errorf("getfn: value is %v, expected %v", string(val), expectedValStr)
			}
			return nil
		})
	}

	t.Logf("hit rate is %v, evacuates %v, entries %v, average time %v, expire count %v\n",
		cache.HitRate(), cache.EvacuateCount(), cache.EntryCount(), cache.AverageAccessTime(), cache.ExpiredCount())

	cache.ResetStatistics()
	t.Logf("hit rate is %v, evacuates %v, entries %v, average time %v, expire count %v\n",
		cache.HitRate(), cache.EvacuateCount(), cache.EntryCount(), cache.AverageAccessTime(), cache.ExpiredCount())
}

func TestOverwrite(t *testing.T) {
	cache := NewCache(1024)
	key := []byte("abcd")
	var val []byte
	cache.Set(key, val, 0)
	val = []byte("efgh")
	cache.Set(key, val, 0)
	val = append(val, 'i')
	cache.Set(key, val, 0)
	if count := cache.OverwriteCount(); count != 0 {
		t.Error("overwrite count is", count, "expected ", 0)
	}
	res, _ := cache.Get(key)
	if string(res) != string(val) {
		t.Error(string(res))
	}
	val = append(val, 'j')
	cache.Set(key, val, 0)
	res, _ = cache.Get(key)
	if string(res) != string(val) {
		t.Error(string(res), "aaa")
	}
	val = append(val, 'k')
	cache.Set(key, val, 0)
	res, _ = cache.Get(key)
	if string(res) != "efghijk" {
		t.Error(string(res))
	}
	val = append(val, 'l')
	cache.Set(key, val, 0)
	res, _ = cache.Get(key)
	if string(res) != "efghijkl" {
		t.Error(string(res))
	}
	val = append(val, 'm')
	cache.Set(key, val, 0)
	if count := cache.OverwriteCount(); count != 3 {
		t.Error("overwrite count is", count, "expected ", 3)
	}
}

func TestGetOrSet(t *testing.T) {
	cache := NewCache(1024)
	key := []byte("abcd")
	val := []byte("efgh")

	r, err := cache.GetOrSet(key, val, 10)
	if err != nil || r != nil {
		t.Errorf("Expected to have nils: value=%v, err=%v", string(r), err)
	}

	// check entry
	r, err = cache.Get(key)
	if err != nil || string(r) != "efgh" {
		t.Errorf("Expected to have val=%v and err != nil, got: value=%v, err=%v", string(val), string(r), err)
	}

	// call twice for the same key
	val = []byte("xxxx")
	r, err = cache.GetOrSet(key, val, 10)
	if err != nil || string(r) != "efgh" {
		t.Errorf("Expected to get old record, got: value=%v, err=%v", string(r), err)
	}
	err = cache.GetFn(key, func(val []byte) error {
		if string(val) != "efgh" {
			t.Errorf("getfn: Expected to get old record, got: value=%v, err=%v", string(r), err)
		}
		return nil
	})
	if err != nil {
		t.Errorf("did not expect error from GetFn, got: %s", err)
	}
}

func TestGetWithExpiration(t *testing.T) {
	cache := NewCache(1024)
	key := []byte("abcd")
	val := []byte("efgh")
	err := cache.Set(key, val, 2)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}

	res, expiry, err := cache.GetWithExpiration(key)
	var expireTime time.Time
	var startTime = time.Now()
	for {
		_, _, err := cache.GetWithExpiration(key)
		expireTime = time.Now()
		if err != nil {
			break
		}
		if time.Now().Unix() > int64(expiry+1) {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}
	if time.Second > expireTime.Sub(startTime) || 3*time.Second < expireTime.Sub(startTime) {
		t.Error("Cache should expire within a second of the expire time")
	}

	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	if !bytes.Equal(val, res) {
		t.Fatalf("%s should be the same as %s but isn't", res, val)
	}
}

func TestExpire(t *testing.T) {
	cache := NewCache(1024)
	key := []byte("abcd")
	val := []byte("efgh")
	err := cache.Set(key, val, 1)
	if err != nil {
		t.Error("err should be nil")
	}
	time.Sleep(time.Second)
	val, err = cache.Get(key)
	if err == nil {
		t.Fatal("key should be expired", string(val))
	}

	cache.ResetStatistics()
	if cache.ExpiredCount() != 0 {
		t.Error("expired count should be zero.")
	}
}

func TestTTL(t *testing.T) {
	cache := NewCache(1024)
	key := []byte("abcd")
	val := []byte("efgh")
	err := cache.Set(key, val, 2)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	time.Sleep(time.Second)
	ttl, err := cache.TTL(key)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	if ttl != 1 {
		t.Fatalf("ttl should be 1, but %d returned", ttl)
	}
}

func TestTouch(t *testing.T) {
	cache := NewCache(1024)
	key1 := []byte("abcd")
	val1 := []byte("efgh")
	key2 := []byte("ijkl")
	val2 := []byte("mnop")
	err := cache.Set(key1, val1, 1)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	err = cache.Set(key2, val2, 1)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	if touched := cache.TouchedCount(); touched != 0 {
		t.Fatalf("touched count should be 0, but %d returned", touched)
	}
	err = cache.Touch(key1, 2)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	time.Sleep(time.Second)
	ttl, err := cache.TTL(key1)
	if err != nil {
		t.Error("err should be nil", err.Error())
	}
	if ttl != 1 {
		t.Fatalf("ttl should be 1, but %d returned", ttl)
	}
	if touched := cache.TouchedCount(); touched != 1 {
		t.Fatalf("touched count should be 1, but %d returned", touched)
	}
	err = cache.Touch(key2, 2)
	if err != ErrNotFound {
		t.Error("error should be ErrNotFound after expiring")
	}
	if touched := cache.TouchedCount(); touched != 1 {
		t.Fatalf("touched count should be 1, but %d returned", touched)
	}
}

func TestAverageAccessTimeWhenUpdateInplace(t *testing.T) {
	cache := NewCache(1024)

	key := []byte("test-key")
	valueLong := []byte("very-long-de-value")
	valueShort := []byte("short")

	err := cache.Set(key, valueLong, 0)
	if err != nil {
		t.Fatal("err should be nil")
	}
	now := time.Now().Unix()
	aat := cache.AverageAccessTime()
	if (now - aat) > 1 {
		t.Fatalf("track average access time error, now:%d, aat:%d", now, aat)
	}

	time.Sleep(time.Second * 4)
	err = cache.Set(key, valueShort, 0)
	if err != nil {
		t.Fatal("err should be nil")
	}
	now = time.Now().Unix()
	aat = cache.AverageAccessTime()
	if (now - aat) > 1 {
		t.Fatalf("track average access time error, now:%d, aat:%d", now, aat)
	}
}

func TestAverageAccessTimeWhenUpdateWithNewSpace(t *testing.T) {
	cache := NewCache(1024)

	key := []byte("test-key")
	valueLong := []byte("very-long-de-value")
	valueShort := []byte("short")

	err := cache.Set(key, valueShort, 0)
	if err != nil {
		t.Fatal("err should be nil")
	}
	now := time.Now().Unix()
	aat := cache.AverageAccessTime()
	if (now - aat) > 1 {
		t.Fatalf("track average access time error, now:%d, aat:%d", now, aat)
	}

	time.Sleep(time.Second * 4)
	err = cache.Set(key, valueLong, 0)
	if err != nil {
		t.Fatal("err should be nil")
	}
	now = time.Now().Unix()
	aat = cache.AverageAccessTime()
	if (now - aat) > 2 {
		t.Fatalf("track average access time error, now:%d, aat:%d", now, aat)
	}
}

func TestLargeEntry(t *testing.T) {
	cacheSize := 512 * 1024
	cache := NewCache(cacheSize)
	key := make([]byte, 65536)
	val := []byte("efgh")
	err := cache.Set(key, val, 0)
	if err != ErrLargeKey {
		t.Error("large key should return ErrLargeKey")
	}
	val, err = cache.Get(key)
	if val != nil {
		t.Error("value should be nil when get a big key")
	}
	key = []byte("abcd")
	maxValLen := cacheSize/1024 - ENTRY_HDR_SIZE - len(key)
	val = make([]byte, maxValLen+1)
	err = cache.Set(key, val, 0)
	if err != ErrLargeEntry {
		t.Error("err should be ErrLargeEntry", err)
	}
	val = make([]byte, maxValLen-2)
	err = cache.Set(key, val, 0)
	if err != nil {
		t.Error(err)
	}
	val = append(val, 0)
	err = cache.Set(key, val, 0)
	if err != nil {
		t.Error(err)
	}
	val = append(val, 0)
	err = cache.Set(key, val, 0)
	if err != nil {
		t.Error(err)
	}
	if cache.OverwriteCount() != 1 {
		t.Errorf("over write count should be one, actual: %d.", cache.OverwriteCount())
	}
	val = append(val, 0)
	err = cache.Set(key, val, 0)
	if err != ErrLargeEntry {
		t.Error("err should be ErrLargeEntry", err)
	}

	cache.ResetStatistics()
	if cache.OverwriteCount() != 0 {
		t.Error("over write count should be zero.")
	}
}

func TestInt64Key(t *testing.T) {
	cache := NewCache(1024)
	err := cache.SetInt(1, []byte("abc"), 3)
	if err != nil {
		t.Error("err should be nil")
	}
	err = cache.SetInt(2, []byte("cde"), 3)
	if err != nil {
		t.Error("err should be nil")
	}
	val, err := cache.GetInt(1)
	if err != nil {
		t.Error("err should be nil")
	}
	if !bytes.Equal(val, []byte("abc")) {
		t.Error("value not equal")
	}
	time.Sleep(2 * time.Second)
	val, expiry, err := cache.GetIntWithExpiration(1)
	if err != nil {
		t.Error("err should be nil")
	}
	if !bytes.Equal(val, []byte("abc")) {
		t.Error("value not equal")
	}
	now := time.Now()
	if expiry != uint32(now.Unix()+1) {
		t.Errorf("Expiry should one second in the future but was %v", now)
	}

	affected := cache.DelInt(1)
	if !affected {
		t.Error("del should return affected true")
	}
	_, err = cache.GetInt(1)
	if err != ErrNotFound {
		t.Error("error should be ErrNotFound after being deleted")
	}
}

func TestIterator(t *testing.T) {
	cache := NewCache(1024)
	count := 10000
	for i := 0; i < count; i++ {
		err := cache.Set([]byte(fmt.Sprintf("%d", i)), []byte(fmt.Sprintf("val%d", i)), 0)
		if err != nil {
			t.Error(err)
		}
	}
	// Set some value that expires to make sure expired entry is not returned.
	cache.Set([]byte("abc"), []byte("def"), 1)
	time.Sleep(2 * time.Second)
	it := cache.NewIterator()
	for i := 0; i < count; i++ {
		entry := it.Next()
		if entry == nil {
			t.Fatalf("entry is nil for %d", i)
		}
		if string(entry.Value) != "val"+string(entry.Key) {
			t.Fatalf("entry key value not match %s %s", entry.Key, entry.Value)
		}
	}
	e := it.Next()
	if e != nil {
		t.Fail()
	}
}

func TestSetLargerEntryDeletesWrongEntry(t *testing.T) {
	cachesize := 512 * 1024
	cache := NewCache(cachesize)

	value1 := "aaa"
	key1 := []byte("key1")
	value := value1
	cache.Set(key1, []byte(value), 0)

	it := cache.NewIterator()
	entry := it.Next()
	if !bytes.Equal(entry.Key, key1) {
		t.Fatalf("key %s not equal to %s", entry.Key, key1)
	}
	if !bytes.Equal(entry.Value, []byte(value)) {
		t.Fatalf("value %s not equal to %s", entry.Value, value)
	}
	entry = it.Next()
	if entry != nil {
		t.Fatalf("expected nil entry but got %s %s", entry.Key, entry.Value)
	}

	value = value1 + "XXXXXX"
	cache.Set(key1, []byte(value), 0)

	value = value1 + "XXXXYYYYYYY"
	cache.Set(key1, []byte(value), 0)
	it = cache.NewIterator()
	entry = it.Next()
	if !bytes.Equal(entry.Key, key1) {
		t.Fatalf("key %s not equal to %s", entry.Key, key1)
	}
	if !bytes.Equal(entry.Value, []byte(value)) {
		t.Fatalf("value %s not equal to %s", entry.Value, value)
	}
	entry = it.Next()
	if entry != nil {
		t.Fatalf("expected nil entry but got %s %s", entry.Key, entry.Value)
	}
}

func TestRace(t *testing.T) {
	cache := NewCache(minBufSize)
	inUse := 8
	wg := sync.WaitGroup{}
	var iters int64 = 1000

	wg.Add(6)
	addFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			err := cache.SetInt(int64(mrand.Intn(inUse)), []byte("abc"), 1)
			if err != nil {
				t.Errorf("err: %s", err)
			}
		}
		wg.Done()
	}
	getFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			_, _ = cache.GetInt(int64(mrand.Intn(inUse))) //it will likely error w/ delFunc running too
		}
		wg.Done()
	}
	delFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			cache.DelInt(int64(mrand.Intn(inUse)))
		}
		wg.Done()
	}
	evacFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			_ = cache.EvacuateCount()
			_ = cache.ExpiredCount()
			_ = cache.EntryCount()
			_ = cache.AverageAccessTime()
			_ = cache.HitCount()
			_ = cache.LookupCount()
			_ = cache.HitRate()
			_ = cache.OverwriteCount()
		}
		wg.Done()
	}
	resetFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			cache.ResetStatistics()
		}
		wg.Done()
	}
	clearFunc := func() {
		var i int64
		for i = 0; i < iters; i++ {
			cache.Clear()
		}
		wg.Done()
	}

	go addFunc()
	go getFunc()
	go delFunc()
	go evacFunc()
	go resetFunc()
	go clearFunc()
	wg.Wait()
}

func TestConcurrentSet(t *testing.T) {
	var wg sync.WaitGroup
	cache := NewCache(256 * 1024 * 1024)
	N := 4000
	routines := 50
	wg.Add(routines)
	for k := 0; k < routines; k++ {
		go func(fact int) {
			defer wg.Done()
			for i := N * fact; i < (fact+1)*N; i++ {
				var key, value [8]byte

				binary.LittleEndian.PutUint64(key[:], uint64(i))
				binary.LittleEndian.PutUint64(value[:], uint64(i*2))
				cache.Set(key[:], value[:], 0)
			}
		}(k)
	}
	wg.Wait()
	for i := 0; i < routines*N; i++ {
		var key, value [8]byte

		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.GetWithBuf(key[:], value[:])
		var num uint64
		binary.Read(bytes.NewBuffer(value[:]), binary.LittleEndian, &num)
		if num != uint64(i*2) {
			t.Fatalf("key %d not equal to %d", int(num), (i * 2))
		}
	}
}

func TestEvacuateCount(t *testing.T) {
	cache := NewCache(1024 * 1024)
	n := 100000
	for i := 0; i < n; i++ {
		err := cache.Set([]byte(strconv.Itoa(i)), []byte("A"), 0)
		if err != nil {
			log.Fatal(err)
		}
	}
	missingItems := 0
	for i := 0; i < n; i++ {
		res, err := cache.Get([]byte(strconv.Itoa(i)))
		if err == ErrNotFound || (err == nil && string(res) != "A") {
			missingItems++
		} else if err != nil {
			log.Fatal(err)
		}
	}
	if cache.EntryCount()+cache.EvacuateCount() != int64(n) {
		t.Fatal(cache.EvacuateCount(), cache.EvacuateCount())
	}
}

func BenchmarkCacheSet(b *testing.B) {
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], make([]byte, 8), 0)
	}
}
func BenchmarkParallelCacheSet(b *testing.B) {
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte

	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		b.ReportAllocs()

		for pb.Next() {
			binary.LittleEndian.PutUint64(key[:], uint64(counter))
			cache.Set(key[:], make([]byte, 8), 0)
			counter = counter + 1
		}
	})
}

func BenchmarkMapSet(b *testing.B) {
	m := make(map[string][]byte)
	var key [8]byte
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		m[string(key[:])] = make([]byte, 8)
	}
}

func BenchmarkCacheGet(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	buf := make([]byte, 64)
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], buf, 0)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Get(key[:])
	}
}

func BenchmarkCacheGetFn(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	buf := make([]byte, 64)
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], buf, 0)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		_ = cache.GetFn(key[:], func(val []byte) error {
			_ = val
			return nil
		})
	}
	b.Logf("b.N: %d; hit rate: %f", b.N, cache.HitRate())
}

func BenchmarkParallelCacheGet(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	buf := make([]byte, 64)
	var key [8]byte
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], buf, 0)
	}
	b.StartTimer()
	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		b.ReportAllocs()
		for pb.Next() {
			binary.LittleEndian.PutUint64(key[:], uint64(counter))
			cache.Get(key[:])
			counter = counter + 1
		}
	})
}

func BenchmarkCacheGetWithBuf(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	buf := make([]byte, 64)
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], buf, 0)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.GetWithBuf(key[:], buf)
	}
}

func BenchmarkParallelCacheGetWithBuf(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	buf := make([]byte, 64)
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], buf, 0)
	}
	b.StartTimer()

	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		b.ReportAllocs()
		for pb.Next() {
			binary.LittleEndian.PutUint64(key[:], uint64(counter))
			cache.GetWithBuf(key[:], buf)
			counter = counter + 1
		}
	})
}

func BenchmarkCacheGetWithExpiration(b *testing.B) {
	b.StopTimer()
	cache := NewCache(256 * 1024 * 1024)
	var key [8]byte
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.Set(key[:], make([]byte, 8), 0)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		cache.GetWithExpiration(key[:])
	}
}

func BenchmarkMapGet(b *testing.B) {
	b.StopTimer()
	m := make(map[string][]byte)
	var key [8]byte
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		m[string(key[:])] = make([]byte, 8)
	}
	b.StartTimer()
	var hitCount int64
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		if m[string(key[:])] != nil {
			hitCount++
		}
	}
}

func BenchmarkHashFunc(b *testing.B) {
	key := make([]byte, 8)
	rand.Read(key)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hashFunc(key)
	}
}

func TestConcurrentGetTTL(t *testing.T) {
	cache := NewCache(256 * 1024 * 1024)
	primaryKey := []byte("hello")
	primaryVal := []byte("world")
	cache.Set(primaryKey, primaryVal, 2)

	// Do concurrent mutation by adding various keys.
	for i := 0; i < 1000; i++ {
		go func(idx int) {
			keyValue := []byte(fmt.Sprintf("counter_%d", idx))
			cache.Set(keyValue, keyValue, 0)
		}(i)
	}

	// While trying to read the TTL.
	_, err := cache.TTL(primaryKey)
	if err != nil {
		t.Fatalf("Failed to get the TTL with an error: %+v", err)
	}
}
