package freecache

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestAdmissionPolicy_NeverAdmit(t *testing.T) {
	cache := NewCacheWithAdmissionPolicy(minBufSize, NewNeverAdmitPolicy())
	if cache.AdmissionPolicy() == nil {
		t.Fatal("expected non-nil admission policy")
	}

	key := []byte("hello")
	val := []byte("world")

	// Set should fail with ErrNotAdmitted
	err := cache.Set(key, val, 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted, got: %v", err)
	}
	if !errors.Is(err, ErrEntryNotAdmitted) {
		t.Fatalf("expected ErrEntryNotAdmitted alias, got: %v", err)
	}

	// Reject count should be 1
	if cache.RejectCount() != 1 {
		t.Fatalf("expected RejectCount == 1, got %d", cache.RejectCount())
	}
	if cache.EntryCount() != 0 {
		t.Fatalf("expected EntryCount == 0, got %d", cache.EntryCount())
	}

	// Get should return ErrNotFound
	_, err = cache.Get(key)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	// SetInt should fail with ErrNotAdmitted
	err = cache.SetInt(12345, val, 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted from SetInt, got: %v", err)
	}

	// GetOrSet should fail with ErrNotAdmitted
	retVal, err := cache.GetOrSet(key, val, 0)
	if !errors.Is(err, ErrNotAdmitted) || retVal != nil {
		t.Fatalf("expected ErrNotAdmitted from GetOrSet, got: %v", err)
	}

	// SetAndGet should fail with ErrNotAdmitted
	retVal, found, err := cache.SetAndGet(key, val, 0)
	if !errors.Is(err, ErrNotAdmitted) || found || retVal != nil {
		t.Fatalf("expected ErrNotAdmitted from SetAndGet, got: %v", err)
	}

	// Update should fail with ErrNotAdmitted when updater replaces
	found, replaced, err := cache.Update(key, func(value []byte, found bool) ([]byte, bool, int) {
		return val, true, 0
	})
	if !errors.Is(err, ErrNotAdmitted) || found || !replaced {
		t.Fatalf("expected ErrNotAdmitted from Update, got: %v", err)
	}
}

func TestAdmissionPolicy_AlwaysAdmit(t *testing.T) {
	cache := NewCacheWithAdmissionPolicy(minBufSize, NewAlwaysAdmitPolicy())

	key := []byte("foo")
	val := []byte("bar")

	err := cache.Set(key, val, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := cache.Get(key)
	if err != nil || !bytes.Equal(got, val) {
		t.Fatalf("expected %s, got %s (err: %v)", val, got, err)
	}

	if cache.RejectCount() != 0 {
		t.Fatalf("expected RejectCount == 0, got %d", cache.RejectCount())
	}
}

func TestAdmissionPolicy_Func(t *testing.T) {
	// Admit only keys starting with "allowed-"
	fn := AdmissionPolicyFunc(func(key []byte, valLen int, expireSeconds int) bool {
		return bytes.HasPrefix(key, []byte("allowed-"))
	})

	cache := NewCache(minBufSize)
	cache.SetAdmissionPolicy(fn)

	// Disallowed key
	err := cache.Set([]byte("blocked-key"), []byte("val"), 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted, got: %v", err)
	}

	// Allowed key
	err = cache.Set([]byte("allowed-key"), []byte("val"), 0)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	got, err := cache.Get([]byte("allowed-key"))
	if err != nil || !bytes.Equal(got, []byte("val")) {
		t.Fatalf("unexpected value or error: %v", err)
	}
}

func TestAdmissionPolicy_Size(t *testing.T) {
	policy := NewSizeAdmissionPolicy(10)
	if policy.MaxValSize() != 10 {
		t.Fatalf("expected MaxValSize 10, got %d", policy.MaxValSize())
	}

	cache := NewCacheWithAdmissionPolicy(minBufSize, policy)

	// Value of 5 bytes (<= 10) -> admitted
	err := cache.Set([]byte("k1"), []byte("12345"), 0)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Value of 15 bytes (> 10) -> rejected
	err = cache.Set([]byte("k2"), []byte("123456789012345"), 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted, got: %v", err)
	}

	// Update existing k1 with value of 20 bytes -> rejected
	err = cache.Set([]byte("k1"), []byte("12345678901234567890"), 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted on oversized update, got: %v", err)
	}

	// k1 still has its original value
	val, err := cache.Get([]byte("k1"))
	if err != nil || !bytes.Equal(val, []byte("12345")) {
		t.Fatalf("expected original value '12345', got %s", val)
	}
}

func TestAdmissionPolicy_Probabilistic(t *testing.T) {
	// Zero rate: never admit
	zeroPolicy := NewProbabilisticAdmissionPolicy(-0.5)
	if zeroPolicy.Rate() != 0.0 {
		t.Fatalf("expected clamped rate 0.0, got %f", zeroPolicy.Rate())
	}
	cacheZero := NewCacheWithAdmissionPolicy(minBufSize, zeroPolicy)
	for i := 0; i < 100; i++ {
		err := cacheZero.Set([]byte(fmt.Sprintf("key-%d", i)), []byte("val"), 0)
		if !errors.Is(err, ErrNotAdmitted) {
			t.Fatalf("expected ErrNotAdmitted for zero rate, got %v", err)
		}
	}
	if cacheZero.RejectCount() != 100 {
		t.Fatalf("expected 100 rejections, got %d", cacheZero.RejectCount())
	}

	// 1.0 rate: always admit
	fullPolicy := NewProbabilisticAdmissionPolicy(1.5)
	if fullPolicy.Rate() != 1.0 {
		t.Fatalf("expected clamped rate 1.0, got %f", fullPolicy.Rate())
	}
	cacheFull := NewCacheWithAdmissionPolicy(minBufSize, fullPolicy)
	for i := 0; i < 100; i++ {
		err := cacheFull.Set([]byte(fmt.Sprintf("key-%d", i)), []byte("val"), 0)
		if err != nil {
			t.Fatalf("expected nil error for 1.0 rate, got %v", err)
		}
	}
	if cacheFull.RejectCount() != 0 {
		t.Fatalf("expected 0 rejections, got %d", cacheFull.RejectCount())
	}

	// 0.5 rate: should admit a reasonable portion
	halfPolicy := NewProbabilisticAdmissionPolicy(0.5)
	cacheHalf := NewCacheWithAdmissionPolicy(minBufSize, halfPolicy)
	admitted := 0
	total := 2000
	for i := 0; i < total; i++ {
		err := cacheHalf.Set([]byte(fmt.Sprintf("rand-key-%d", i)), []byte("val"), 0)
		if err == nil {
			admitted++
		}
	}
	// With 2000 samples and p=0.5, admitted should be roughly between 700 and 1300
	if admitted < 700 || admitted > 1300 {
		t.Fatalf("expected approximately 50%% admissions, got %d out of %d", admitted, total)
	}
}

func TestAdmissionPolicy_BloomFilter(t *testing.T) {
	policy := NewBloomFilterAdmissionPolicy(1000, 1000)
	cache := NewCacheWithAdmissionPolicy(minBufSize, policy)

	key := []byte("frequent-key")
	val1 := []byte("value-1")
	val2 := []byte("value-2")

	// 1st Set: should be rejected as a one-hit wonder
	err := cache.Set(key, val1, 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected 1st Set to return ErrNotAdmitted, got: %v", err)
	}
	if cache.RejectCount() != 1 {
		t.Fatalf("expected RejectCount == 1, got %d", cache.RejectCount())
	}
	_, err = cache.Get(key)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after rejection, got: %v", err)
	}

	// 2nd Set: key has been seen before in bloom filter -> admitted!
	err = cache.Set(key, val1, 0)
	if err != nil {
		t.Fatalf("expected 2nd Set to succeed, got: %v", err)
	}
	got, err := cache.Get(key)
	if err != nil || !bytes.Equal(got, val1) {
		t.Fatalf("expected %s, got %s (err: %v)", val1, got, err)
	}

	// 3rd Set (Update): should be admitted via AdmitUpdate
	err = cache.Set(key, val2, 0)
	if err != nil {
		t.Fatalf("expected update to succeed, got: %v", err)
	}
	got, err = cache.Get(key)
	if err != nil || !bytes.Equal(got, val2) {
		t.Fatalf("expected updated %s, got %s", val2, got)
	}

	// Test Reset
	if policy.Count() <= 0 {
		t.Fatalf("expected policy.Count() > 0, got %d", policy.Count())
	}
	policy.Reset()
	if policy.Count() != 0 {
		t.Fatalf("expected policy.Count() == 0 after Reset, got %d", policy.Count())
	}

	// Test cache.Clear() resets bloom filter
	cache.Clear()
	if policy.Count() != 0 {
		t.Fatalf("expected policy.Count() == 0 after cache.Clear(), got %d", policy.Count())
	}
}

func TestAdmissionPolicy_BloomFilterResetThreshold(t *testing.T) {
	// Small reset threshold
	policy := NewBloomFilterAdmissionPolicy(100, 20)
	for i := 0; i < 50; i++ {
		policy.Admit([]byte(fmt.Sprintf("unique-%d", i)), 0, 0)
	}
	// Count should have reset at least once, staying <= threshold
	if policy.Count() > 20 {
		t.Fatalf("expected Count <= 20 due to resets, got %d", policy.Count())
	}
}

func TestAdmissionPolicy_AdmitUpdates(t *testing.T) {
	// Under a NeverAdmitPolicy wrapped with AdmitUpdates:
	// New keys should be rejected, but updates to pre-existing keys should succeed.
	cache := NewCache(minBufSize)

	key := []byte("existing")
	err := cache.Set(key, []byte("v1"), 0)
	if err != nil {
		t.Fatalf("initial set failed: %v", err)
	}

	// Now apply NeverAdmit wrapped with AdmitUpdates
	policy := AdmitUpdates(NewNeverAdmitPolicy())
	cache.SetAdmissionPolicy(policy)

	// Updating existing key should succeed
	err = cache.Set(key, []byte("v2"), 0)
	if err != nil {
		t.Fatalf("expected update to existing key to succeed, got: %v", err)
	}
	got, err := cache.Get(key)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("expected 'v2', got %s", got)
	}

	// New key should be rejected
	err = cache.Set([]byte("new-key"), []byte("v"), 0)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted for new key, got: %v", err)
	}
}

type mockLifecyclePolicy struct {
	mu          sync.Mutex
	admissions  map[string]int
	accesses    map[string]int
	evictions   map[string]int
	alwaysAdmit bool
}

func newMockLifecyclePolicy() *mockLifecyclePolicy {
	return &mockLifecyclePolicy{
		admissions:  make(map[string]int),
		accesses:    make(map[string]int),
		evictions:   make(map[string]int),
		alwaysAdmit: true,
	}
}

func (m *mockLifecyclePolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admissions[string(key)]++
	return m.alwaysAdmit
}

func (m *mockLifecyclePolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return m.Admit(key, valLen, expireSeconds)
}

func (m *mockLifecyclePolicy) RecordAccess(key []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accesses[string(key)]++
}

func (m *mockLifecyclePolicy) RecordEviction(key []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictions[string(key)]++
}

func TestAdmissionPolicy_LifecycleHooks(t *testing.T) {
	mock := newMockLifecyclePolicy()
	cache := NewCacheWithAdmissionPolicy(minBufSize, mock)

	k1 := []byte("hook-key-1")
	k2 := []byte("hook-key-2")

	// Set both keys
	if err := cache.Set(k1, []byte("v1"), 0); err != nil {
		t.Fatalf("unexpected set err: %v", err)
	}
	if err := cache.Set(k2, []byte("v2"), 0); err != nil {
		t.Fatalf("unexpected set err: %v", err)
	}

	// Verify Admit called
	mock.mu.Lock()
	if mock.admissions[string(k1)] != 1 || mock.admissions[string(k2)] != 1 {
		t.Fatalf("expected 1 admission each, got %v", mock.admissions)
	}
	mock.mu.Unlock()

	// Call Get, GetWithBuf, GetFn, MultiGet
	if _, err := cache.Get(k1); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	if _, err := cache.GetWithBuf(k1, buf); err != nil {
		t.Fatal(err)
	}
	if err := cache.GetFn(k1, func(v []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	vals, errs := cache.MultiGet([][]byte{k1, k2})
	if len(vals) != 2 || errs[0] != nil || errs[1] != nil {
		t.Fatalf("multi get failed: %v", errs)
	}

	// Verify RecordAccess counts
	mock.mu.Lock()
	if mock.accesses[string(k1)] != 4 { // Get + GetWithBuf + GetFn + MultiGet
		t.Fatalf("expected 4 accesses on k1, got %d", mock.accesses[string(k1)])
	}
	if mock.accesses[string(k2)] != 1 { // MultiGet
		t.Fatalf("expected 1 access on k2, got %d", mock.accesses[string(k2)])
	}
	mock.mu.Unlock()

	// Peek should NOT record access
	if _, err := cache.Peek(k1); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	if mock.accesses[string(k1)] != 4 {
		t.Fatalf("Peek recorded access unexpectedly: %d", mock.accesses[string(k1)])
	}
	mock.mu.Unlock()

	// Del should record eviction
	affected := cache.Del(k1)
	if !affected {
		t.Fatal("expected Del to return true")
	}
	mock.mu.Lock()
	if mock.evictions[string(k1)] != 1 {
		t.Fatalf("expected 1 eviction for k1, got %d", mock.evictions[string(k1)])
	}
	mock.mu.Unlock()
}

func TestAdmissionPolicy_DynamicSetAndReset(t *testing.T) {
	cache := NewCache(minBufSize)

	k := []byte("dynamic")
	if err := cache.Set(k, []byte("1"), 0); err != nil {
		t.Fatal(err)
	}

	// Change to NeverAdmit
	cache.SetAdmissionPolicy(NewNeverAdmitPolicy())
	if err := cache.Set([]byte("other"), []byte("2"), 0); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("expected ErrNotAdmitted after setting policy, got: %v", err)
	}

	// Revert to nil policy
	cache.SetAdmissionPolicy(nil)
	if err := cache.Set([]byte("other"), []byte("2"), 0); err != nil {
		t.Fatalf("expected success after clearing policy, got: %v", err)
	}

	// Test ResetStatistics
	if cache.RejectCount() != 1 {
		t.Fatalf("expected RejectCount == 1, got %d", cache.RejectCount())
	}
	cache.ResetStatistics()
	if cache.RejectCount() != 0 {
		t.Fatalf("expected RejectCount == 0 after ResetStatistics, got %d", cache.RejectCount())
	}
}

func TestAdmissionPolicy_Concurrent(t *testing.T) {
	bloom := NewBloomFilterAdmissionPolicy(10000, 10000)
	cache := NewCacheWithAdmissionPolicy(minBufSize*2, bloom)

	var wg sync.WaitGroup
	workers := 20
	iterations := 200

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := []byte(fmt.Sprintf("worker-%d-item-%d", workerID, i%50))
				val := []byte(fmt.Sprintf("val-%d", i))
				// First write may be rejected, second write for same key will succeed
				_ = cache.Set(key, val, 0)
				_, _ = cache.Get(key)
			}
		}(w)
	}

	wg.Wait()

	if cache.RejectCount() <= 0 {
		t.Fatalf("expected RejectCount > 0, got %d", cache.RejectCount())
	}
	if cache.EntryCount() <= 0 {
		t.Fatalf("expected EntryCount > 0, got %d", cache.EntryCount())
	}
}
