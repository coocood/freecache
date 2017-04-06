package freecache

const (
	offset64 uint64 = 14695981039346656037
	prime64  uint64 = 1099511628211
)

// sum64 is an in-lined version of Go's built-in fnv 64a hash.
// https://golang.org/pkg/hash/fnv/
//
// These lines of code are equivalent:
//
// hash := sum64(data)
//
// h := fnv.New64a()
// h.Write(data)
// hash := h.Sum64()
//
// but the former creates no allocations.
func sum64(data []byte) uint64 {
	hash := offset64
	for _, c := range data {
		hash ^= uint64(c)
		hash *= prime64
	}
	return hash
}
