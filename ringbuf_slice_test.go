package freecache

import (
	"bytes"
	"math"
	"testing"
)

func TestRingBufSliceRejectsInvalidRange(t *testing.T) {
	factories := []struct {
		name string
		make func() RingBuf
	}{
		{"partial", func() RingBuf { rb := NewRingBuf(8, 10); _, _ = rb.Write([]byte("abcd")); return rb }},
		{"wrapped", func() RingBuf {
			rb := NewRingBuf(8, 10)
			_, _ = rb.Write([]byte("abcdefgh"))
			_, _ = rb.Write([]byte("ijk"))
			return rb
		}},
		{"near_max_offset", func() RingBuf { rb := NewRingBuf(8, math.MaxInt64-8); _, _ = rb.Write([]byte("abcd")); return rb }},
		{"empty", func() RingBuf { return NewRingBuf(8, 10) }},
		{"zero_capacity", func() RingBuf { return NewRingBuf(0, 10) }},
	}
	for _, factory := range factories {
		rb := factory.make()
		cases := []struct {
			name        string
			off, length int64
		}{
			{"before_begin", rb.Begin() - 1, 0},
			{"past_end", rb.End() + 1, 0},
			{"negative", rb.Begin(), -1},
			{"min_length", rb.Begin(), math.MinInt64},
			{"at_end_nonempty", rb.End(), 1},
			{"past_logical_end", rb.Begin(), rb.End() - rb.Begin() + 1},
			{"huge", rb.Begin(), math.MaxInt64},
			{"overflow_sum", rb.End(), math.MaxInt64},
		}
		for _, tc := range cases {
			t.Run(factory.name+"/"+tc.name, func(t *testing.T) {
				before := rb.Dump()
				begin, end, index := rb.begin, rb.end, rb.index
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Errorf("invalid range panicked: %v", recovered)
					}
					if !bytes.Equal(before, rb.Dump()) || rb.begin != begin || rb.end != end || rb.index != index {
						t.Error("Slice mutated buffer")
					}
				}()
				got, err := rb.Slice(tc.off, tc.length)
				if err != ErrOutOfRange || got != nil {
					t.Errorf("got bytes %v, error %v; want nil, ErrOutOfRange", got, err)
				}
			})
		}
	}
}

func TestRingBufSliceMatchesStream(t *testing.T) {
	// Oracle is the retained suffix of a linear byte stream, not ReadAt or
	// getDataOff. Include every valid subrange after each write and after reset.
	for size := 1; size <= 12; size++ {
		for _, initial := range []int64{-20, 0, 100, math.MaxInt64 - 200} {
			rb := NewRingBuf(size, initial)
			var stream []byte
			for step := 0; step < 15; step++ {
				data := bytes.Repeat([]byte{byte(step + 1)}, step%(size+1))
				_, _ = rb.Write(data)
				stream = append(stream, data...)
				retained := stream
				if len(retained) > size {
					retained = retained[len(retained)-size:]
				}
				for start := 0; start <= len(retained); start++ {
					for length := 0; length <= len(retained)-start; length++ {
						got, err := rb.Slice(rb.Begin()+int64(start), int64(length))
						if err != nil || !bytes.Equal(got, retained[start:start+length]) {
							t.Fatalf("size%d initial%d step%d start%d length%d: got %v/%v want %v", size, initial, step, start, length, got, err, retained[start:start+length])
						}
						if len(got) != cap(got) {
							t.Fatal("slice exposes capacity beyond requested range")
						}
					}
				}
				if step == 7 {
					rb.Reset(initial)
					stream = nil
				}
			}
		}
	}
}

func TestRingBufSliceAliasingContract(t *testing.T) {
	rb := NewRingBuf(8, 0)
	_, _ = rb.Write([]byte("abcdefgh"))
	_, _ = rb.Write([]byte("ij"))
	contiguous, err := rb.Slice(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	contiguous[0] = 'X'
	if !rb.EqualAt([]byte("Xd"), 2) {
		t.Fatal("contiguous slice should alias ring")
	}
	wrapped, err := rb.Slice(7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if string(wrapped) != "hij" {
		t.Fatalf("got %q", wrapped)
	}
	wrapped[0] = 'Y'
	if !rb.EqualAt([]byte("hij"), 7) {
		t.Fatal("wrapped slice should be independent")
	}
}
