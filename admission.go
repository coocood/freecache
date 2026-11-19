package freecache

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
)

// AdmissionPolicy decides whether an entry should be admitted into the cache.
type AdmissionPolicy interface {
	// Admit returns true if the entry should be admitted into the cache,
	// or false if it should be rejected.
	Admit(key []byte, valLen int, expireSeconds int) bool
}

// UpdateAwareAdmissionPolicy is an optional extension to AdmissionPolicy
// that allows differentiating between new key admissions and updates to existing keys.
type UpdateAwareAdmissionPolicy interface {
	AdmissionPolicy
	// AdmitUpdate returns true if an update to an existing key should be admitted.
	AdmitUpdate(key []byte, valLen int, expireSeconds int) bool
}

// AccessAwareAdmissionPolicy is an optional extension to AdmissionPolicy
// that receives notifications when an existing key is accessed in the cache.
type AccessAwareAdmissionPolicy interface {
	AdmissionPolicy
	// RecordAccess is called when an entry is successfully accessed.
	RecordAccess(key []byte)
}

// EvictionAwareAdmissionPolicy is an optional extension to AdmissionPolicy
// that receives notifications when an entry is evicted or deleted from the cache.
type EvictionAwareAdmissionPolicy interface {
	AdmissionPolicy
	// RecordEviction is called when an entry is evicted or deleted.
	RecordEviction(key []byte)
}

// AdmissionPolicyFunc is an adapter to allow the use of ordinary functions as AdmissionPolicy.
type AdmissionPolicyFunc func(key []byte, valLen int, expireSeconds int) bool

// Admit calls f(key, valLen, expireSeconds).
func (f AdmissionPolicyFunc) Admit(key []byte, valLen int, expireSeconds int) bool {
	return f(key, valLen, expireSeconds)
}

// NeverAdmitPolicy rejects all cache admissions.
type NeverAdmitPolicy struct{}

// NewNeverAdmitPolicy returns an AdmissionPolicy that rejects all entries.
func NewNeverAdmitPolicy() AdmissionPolicy {
	return NeverAdmitPolicy{}
}

// Admit always returns false.
func (NeverAdmitPolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	return false
}

// AdmitUpdate always returns false.
func (NeverAdmitPolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return false
}

// AlwaysAdmitPolicy admits all cache entries.
type AlwaysAdmitPolicy struct{}

// NewAlwaysAdmitPolicy returns an AdmissionPolicy that admits all entries.
func NewAlwaysAdmitPolicy() AdmissionPolicy {
	return AlwaysAdmitPolicy{}
}

// Admit always returns true.
func (AlwaysAdmitPolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	return true
}

// AdmitUpdate always returns true.
func (AlwaysAdmitPolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return true
}

// SizeAdmissionPolicy only admits entries whose value size is within a threshold.
type SizeAdmissionPolicy struct {
	maxValSize int
}

// NewSizeAdmissionPolicy returns an AdmissionPolicy that rejects values larger than maxValSize.
func NewSizeAdmissionPolicy(maxValSize int) *SizeAdmissionPolicy {
	return &SizeAdmissionPolicy{maxValSize: maxValSize}
}

// Admit returns true if valLen <= maxValSize.
func (p *SizeAdmissionPolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	return valLen <= p.maxValSize
}

// AdmitUpdate returns true if valLen <= maxValSize.
func (p *SizeAdmissionPolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return valLen <= p.maxValSize
}

// MaxValSize returns the maximum value size threshold.
func (p *SizeAdmissionPolicy) MaxValSize() int {
	return p.maxValSize
}

// ProbabilisticAdmissionPolicy admits entries based on a configured sampling rate.
type ProbabilisticAdmissionPolicy struct {
	rate      float64
	threshold uint64
	seed      uint64
}

// NewProbabilisticAdmissionPolicy returns an AdmissionPolicy that admits entries with probability rate (0.0 to 1.0).
func NewProbabilisticAdmissionPolicy(rate float64) *ProbabilisticAdmissionPolicy {
	if rate < 0 {
		rate = 0
	} else if rate > 1 {
		rate = 1
	}
	var threshold uint64
	if rate >= 1.0 {
		threshold = math.MaxUint64
	} else if rate <= 0.0 {
		threshold = 0
	} else {
		threshold = uint64(rate * float64(math.MaxUint64))
	}
	return &ProbabilisticAdmissionPolicy{
		rate:      rate,
		threshold: threshold,
		seed:      uint64(time.Now().UnixNano()),
	}
}

// Admit probabilistically decides whether to admit the entry.
func (p *ProbabilisticAdmissionPolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	if p.threshold == math.MaxUint64 {
		return true
	}
	if p.threshold == 0 {
		return false
	}
	z := atomic.AddUint64(&p.seed, 0x9e3779b97f4a7c15)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	val := z ^ (z >> 31)
	return val < p.threshold
}

// AdmitUpdate always admits updates to existing entries.
func (p *ProbabilisticAdmissionPolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return true
}

// Rate returns the configured admission rate.
func (p *ProbabilisticAdmissionPolicy) Rate() float64 {
	return p.rate
}

const bloomShardCount = 256

type bloomShard struct {
	mu             sync.Mutex
	bits           []uint64
	numBits        uint64
	numHashes      int
	count          int
	resetThreshold int
}

// BloomFilterAdmissionPolicy acts as a Doorkeeper to filter out one-hit wonders.
// Entries must be observed at least twice before being admitted into the cache.
type BloomFilterAdmissionPolicy struct {
	shards         [bloomShardCount]bloomShard
	capacity       int
	falsePosRate   float64
	resetThreshold int
}

// NewBloomFilterAdmissionPolicy creates a Bloom filter admission policy with a default 1% false positive rate.
func NewBloomFilterAdmissionPolicy(capacity int, resetThreshold int) *BloomFilterAdmissionPolicy {
	return NewBloomFilterAdmissionPolicyWithRate(capacity, 0.01, resetThreshold)
}

// NewBloomFilterAdmissionPolicyWithRate creates a Bloom filter admission policy with custom false positive rate.
func NewBloomFilterAdmissionPolicyWithRate(capacity int, falsePositiveRate float64, resetThreshold int) *BloomFilterAdmissionPolicy {
	if capacity <= 0 {
		capacity = 100000
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		falsePositiveRate = 0.01
	}
	if resetThreshold <= 0 {
		resetThreshold = capacity
	}

	shardCap := (capacity + bloomShardCount - 1) / bloomShardCount
	if shardCap < 1 {
		shardCap = 1
	}
	shardReset := (resetThreshold + bloomShardCount - 1) / bloomShardCount
	if shardReset < 1 {
		shardReset = 1
	}

	ln2 := math.Ln2
	mFloat := -1.0 * float64(shardCap) * math.Log(falsePositiveRate) / (ln2 * ln2)
	m := uint64(math.Ceil(mFloat))
	if m < 64 {
		m = 64
	}
	kFloat := (float64(m) / float64(shardCap)) * ln2
	k := int(math.Round(kFloat))
	if k < 1 {
		k = 1
	}
	if k > 30 {
		k = 30
	}
	words := (m + 63) / 64

	p := &BloomFilterAdmissionPolicy{
		capacity:       capacity,
		falsePosRate:   falsePositiveRate,
		resetThreshold: resetThreshold,
	}

	for i := 0; i < bloomShardCount; i++ {
		p.shards[i].bits = make([]uint64, words)
		p.shards[i].numBits = m
		p.shards[i].numHashes = k
		p.shards[i].resetThreshold = shardReset
	}
	return p
}

// Admit checks whether key has been observed before. If not, it records the key and returns false.
// If observed, it returns true to admit the key into the cache.
func (p *BloomFilterAdmissionPolicy) Admit(key []byte, valLen int, expireSeconds int) bool {
	h := xxhash.Sum64(key)
	shardIdx := (h >> 56) & (bloomShardCount - 1)
	shard := &p.shards[shardIdx]

	h1 := uint32(h)
	h2 := uint32(h >> 32)
	if h2 == 0 {
		h2 = 1
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	allSet := true
	for i := 0; i < shard.numHashes; i++ {
		bitIdx := (uint64(h1) + uint64(i)*uint64(h2)) % shard.numBits
		wordIdx := bitIdx / 64
		mask := uint64(1) << (bitIdx % 64)
		if (shard.bits[wordIdx] & mask) == 0 {
			allSet = false
			shard.bits[wordIdx] |= mask
		}
	}

	if allSet {
		return true
	}

	shard.count++
	if shard.resetThreshold > 0 && shard.count >= shard.resetThreshold {
		for i := range shard.bits {
			shard.bits[i] = 0
		}
		shard.count = 0
	}
	return false
}

// AdmitUpdate always admits updates to existing cache entries.
func (p *BloomFilterAdmissionPolicy) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return true
}

// Reset clears all bits and counts in the Bloom filter.
func (p *BloomFilterAdmissionPolicy) Reset() {
	for i := 0; i < bloomShardCount; i++ {
		shard := &p.shards[i]
		shard.mu.Lock()
		for j := range shard.bits {
			shard.bits[j] = 0
		}
		shard.count = 0
		shard.mu.Unlock()
	}
}

// Count returns the approximate number of entries recorded across all shards since the last reset.
func (p *BloomFilterAdmissionPolicy) Count() int {
	total := 0
	for i := 0; i < bloomShardCount; i++ {
		shard := &p.shards[i]
		shard.mu.Lock()
		total += shard.count
		shard.mu.Unlock()
	}
	return total
}

// AdmitUpdates wraps an AdmissionPolicy to always admit updates to existing entries,
// delegating new admissions to the underlying policy.
func AdmitUpdates(policy AdmissionPolicy) AdmissionPolicy {
	if policy == nil {
		return AlwaysAdmitPolicy{}
	}
	return &admitUpdatesWrapper{policy: policy}
}

type admitUpdatesWrapper struct {
	policy AdmissionPolicy
}

func (w *admitUpdatesWrapper) Admit(key []byte, valLen int, expireSeconds int) bool {
	return w.policy.Admit(key, valLen, expireSeconds)
}

func (w *admitUpdatesWrapper) AdmitUpdate(key []byte, valLen int, expireSeconds int) bool {
	return true
}

func (w *admitUpdatesWrapper) RecordAccess(key []byte) {
	if aa, ok := w.policy.(AccessAwareAdmissionPolicy); ok {
		aa.RecordAccess(key)
	}
}

func (w *admitUpdatesWrapper) RecordEviction(key []byte) {
	if ea, ok := w.policy.(EvictionAwareAdmissionPolicy); ok {
		ea.RecordEviction(key)
	}
}
