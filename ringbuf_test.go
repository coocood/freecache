package freecache

import (
	"bytes"
	"math"
	"testing"
)

func TestRingBuf(t *testing.T) {
	rb := NewRingBuf(16, 0)
	for i := 0; i < 2; i++ {
		rb.Write([]byte("fghibbbbccccddde"))
		rb.Write([]byte("fghibbbbc"))
		rb.Resize(16)
		off := rb.Evacuate(9, 3)
		t.Log(string(rb.Dump()))
		if off != rb.End()-3 {
			t.Log(string(rb.Dump()), rb.End())
			t.Fatalf("off got %v", off)
		}
		off = rb.Evacuate(15, 5)
		t.Log(string(rb.Dump()))
		if off != rb.End()-5 {
			t.Fatalf("off got %v", off)
		}
		rb.Resize(64)
		rb.Resize(32)
		data := make([]byte, 5)
		rb.ReadAt(data, off)
		if string(data) != "efghi" {
			t.Fatalf("read at should be efghi, got %v", string(data))
		}

		off = rb.Evacuate(0, 10)
		if off != -1 {
			t.Fatal("evacutate out of range offset should return error")
		}

		/* -- After reset the buffer should behave exactly the same as a new one.
		 *    Hence, run the test once more again with reset buffer. */
		rb.Reset(0)
	}
}

func TestRingBufOverflowNearMaxInt64(t *testing.T) {
	rb := NewRingBuf(16, math.MaxInt64-8)
	if rb.Begin() != math.MaxInt64-8 || rb.End() != math.MaxInt64-8 {
		t.Fatalf("unexpected initial offsets: begin=%v end=%v", rb.Begin(), rb.End())
	}

	payload := []byte("hello")
	n, err := rb.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write failed: n=%v err=%v", n, err)
	}

	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End() >= math.MaxInt64-8 {
		t.Fatalf("offsets should be normalized: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End()-rb.Begin() != int64(len(payload)) {
		t.Fatalf("unexpected used length: got %v want %v", rb.End()-rb.Begin(), len(payload))
	}

	readBuf := make([]byte, len(payload))
	n, err = rb.ReadAt(readBuf, rb.Begin())
	if err != nil || n != len(payload) {
		t.Fatalf("read failed: n=%v err=%v", n, err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("read data mismatch: got %s want %s", readBuf, payload)
	}

	more := []byte("0123456789a")
	n, err = rb.Write(more)
	if err != nil || n != len(more) {
		t.Fatalf("write more failed: n=%v err=%v", n, err)
	}
	if rb.End()-rb.Begin() != 16 {
		t.Fatalf("expected buffer to be full: used=%v", rb.End()-rb.Begin())
	}

	fullBuf := make([]byte, 16)
	_, err = rb.ReadAt(fullBuf, rb.Begin())
	if err != nil {
		t.Fatalf("read full buffer failed: %v", err)
	}
	expected := append(payload, more...)
	if !bytes.Equal(fullBuf, expected) {
		t.Fatalf("full buffer data mismatch: got %s want %s", fullBuf, expected)
	}
}

func TestRingBufWrapAroundBoundary(t *testing.T) {
	rb := NewRingBuf(16, math.MaxInt64-4)
	payload := []byte("abcdefghij")
	n, err := rb.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write failed: n=%v err=%v", n, err)
	}

	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End() <= rb.Begin() {
		t.Fatalf("end must be greater than begin: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End()-rb.Begin() != int64(len(payload)) {
		t.Fatalf("used length mismatch: got %v want %v", rb.End()-rb.Begin(), len(payload))
	}

	readBuf := make([]byte, len(payload))
	_, err = rb.ReadAt(readBuf, rb.Begin())
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("data mismatch: got %s want %s", readBuf, payload)
	}

	sliced, err := rb.Slice(rb.Begin(), int64(len(payload)))
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}
	if !bytes.Equal(sliced, payload) {
		t.Fatalf("sliced data mismatch: got %s want %s", sliced, payload)
	}
}

func TestRingBufEvacuateNearMaxInt64(t *testing.T) {
	rb := NewRingBuf(16, math.MaxInt64-16)
	initialData := []byte("0123456789abcdef")
	_, err := rb.Write(initialData)
	if err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	evacOff := rb.Begin() + 2
	newOff := rb.Evacuate(evacOff, 4)
	if newOff == -1 {
		t.Fatal("evacuate returned -1")
	}
	if newOff != rb.End()-4 {
		t.Fatalf("newOff mismatch: got %v want %v", newOff, rb.End()-4)
	}
	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}

	evacBuf := make([]byte, 4)
	_, err = rb.ReadAt(evacBuf, newOff)
	if err != nil {
		t.Fatalf("read at newOff failed: %v", err)
	}
	if !bytes.Equal(evacBuf, []byte("2345")) {
		t.Fatalf("evacuated data mismatch: got %s want 2345", evacBuf)
	}
}

func TestRingBufSkipNearMaxInt64(t *testing.T) {
	rb := NewRingBuf(16, math.MaxInt64-8)
	rb.Skip(10)
	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End()-rb.Begin() != 10 {
		t.Fatalf("used mismatch: got %v want 10", rb.End()-rb.Begin())
	}
}

func TestRingBufResizeNearMaxInt64(t *testing.T) {
	rb := NewRingBuf(16, math.MaxInt64-8)
	data := []byte("abcdefgh")
	_, err := rb.Write(data)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	rb.Resize(32)
	if rb.Size() != 32 {
		t.Fatalf("expected size 32, got %v", rb.Size())
	}
	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}

	readBuf := make([]byte, len(data))
	_, err = rb.ReadAt(readBuf, rb.Begin())
	if err != nil {
		t.Fatalf("read after resize failed: %v", err)
	}
	if !bytes.Equal(readBuf, data) {
		t.Fatalf("data mismatch after resize: got %s want %s", readBuf, data)
	}
}

func TestRingBufNegativeOffsetsHandling(t *testing.T) {
	rb := NewRingBuf(16, 0)
	rb.begin = math.MaxInt64 - 5
	rb.end = math.MinInt64 + 5

	normalized := rb.checkWrap()
	if !normalized {
		t.Fatal("expected checkWrap to return true on negative offsets")
	}
	if rb.Begin() < 0 || rb.End() < 0 {
		t.Fatalf("offsets must be non-negative: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End() < rb.Begin() {
		t.Fatalf("end must be >= begin: begin=%v end=%v", rb.Begin(), rb.End())
	}
	if rb.End()-rb.Begin() != 11 {
		t.Fatalf("used length mismatch: got %v want 11", rb.End()-rb.Begin())
	}
}
