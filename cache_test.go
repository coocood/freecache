package freecache

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRingCache(t *testing.T) {
	cache := NewCache(1024)
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
	// test expire
	err = cache.Set(key, val, 1)
	if err != nil {
		t.Error("err should be nil")
	}
	time.Sleep(time.Second)
	value, err = cache.Get(key)
	if err == nil {
		t.Fatal("key should be expired", string(value))
	}

	bigKey := make([]byte, 65536)
	err = cache.Set(bigKey, val, 0)
	if err != ErrLargeKey {
		t.Error("large key should return ErrLargeKey")
	}
	value, err = cache.Get(bigKey)
	if value != nil {
		t.Error("value should be nil when get a big key")
	}
	err = cache.Set(key, bigKey, 0)
	if err != ErrLargeEntry {
		t.Error("err should be ErrLargeEntry")
	}
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
	for i := 0; i < n; i += 2 {
		keyStr := fmt.Sprintf("key%v", i)
		valStr := strings.Repeat(keyStr, 10)
		err = cache.Set([]byte(keyStr), []byte(valStr), 0)
		if err != nil {
			t.Error(err)
		}
	}
	hitCount := 0
	for i := 1; i < n; i += 2 {
		keyStr := fmt.Sprintf("key%v", i)
		expectedValStr := strings.Repeat(keyStr, 10)
		value, err = cache.Get([]byte(keyStr))
		if err == nil {
			hitCount++
			if string(value) != expectedValStr {
				t.Errorf("value is %v, expected %v", string(value), expectedValStr)
			}
		}
	}

	t.Logf("hit rate is %v, evacuates %v, entries %v, average time %v\n",
		float64(hitCount)/float64(n/2), cache.EvacuateCount(), cache.EntryCount(), cache.AverageAccessTime())
}
