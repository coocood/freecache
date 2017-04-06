package freecache

import (
	"hash/fnv"
	"testing"
	"testing/quick"
)

func TestInlineFNV64aEquivalenceFuzz(t *testing.T) {
	f := func(data []byte) bool {
		// get standard library FNV hash:
		h := fnv.New64a()
		h.Write(data)
		want := h.Sum64()

		// get our inline FNV hash:
		got := sum64(data)

		return want == got
	}
	cfg := &quick.Config{
		MaxCount: 100000,
	}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatal(err)
	}
}
