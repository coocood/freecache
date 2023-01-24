package freecache

import (
	"encoding/binary"
	"unsafe"
)

// Iterator iterates the entries for the cache.
type Iterator struct {
	cache      *Cache
	segmentIdx int
	slotIdx    int
	entryIdx   int
}

// Entry represents a key/value pair.
type Entry struct {
	Key      []byte
	Value    []byte
	ExpireAt uint32
}

const headerEntry = 4 + 4 + 4

func getEntrySize(b []byte) int {
	return headerEntry + int(binary.LittleEndian.Uint32(b[0:4])) + int(binary.LittleEndian.Uint32(b[4:8]))
}

func encodeEntry(e *Entry) []byte {
	size := headerEntry + len(e.Key) + len(e.Value)
	buf := make([]byte, size)

	// header
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(e.Key)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(e.Value)))
	binary.LittleEndian.PutUint32(buf[8:12], e.ExpireAt)

	// data
	copy(buf[12:], e.Key)
	copy(buf[12+len(e.Key):], e.Value)
	return buf
}

func decodeEntry(b []byte) *Entry {
	ks := binary.LittleEndian.Uint32(b[0:4])
	vs := binary.LittleEndian.Uint32(b[4:8])
	expire := binary.LittleEndian.Uint32(b[8:12])
	return &Entry{
		Key:      b[12 : 12+ks],
		Value:    b[12+ks : 12+ks+vs],
		ExpireAt: expire,
	}
}

// Next returns the next entry for the iterator.
// The order of the entries is not guaranteed.
// If there is no more entries to return, nil will be returned.
func (it *Iterator) Next() *Entry {
	for it.segmentIdx < 256 {
		entry := it.nextForSegment(it.segmentIdx)
		if entry != nil {
			return entry
		}
		it.segmentIdx++
		it.slotIdx = 0
		it.entryIdx = 0
	}
	return nil
}

func (it *Iterator) nextForSegment(segIdx int) *Entry {
	it.cache.locks[segIdx].Lock()
	defer it.cache.locks[segIdx].Unlock()
	seg := &it.cache.segments[segIdx]
	for it.slotIdx < 256 {
		entry := it.nextForSlot(seg, it.slotIdx)
		if entry != nil {
			return entry
		}
		it.slotIdx++
		it.entryIdx = 0
	}
	return nil
}

func (it *Iterator) nextForSlot(seg *segment, slotId int) *Entry {
	slotOff := int32(it.slotIdx) * seg.slotCap
	slot := seg.slotsData[slotOff : slotOff+seg.slotLens[it.slotIdx] : slotOff+seg.slotCap]
	for it.entryIdx < len(slot) {
		ptr := slot[it.entryIdx]
		it.entryIdx++
		now := seg.timer.Now()
		var hdrBuf [ENTRY_HDR_SIZE]byte
		seg.rb.ReadAt(hdrBuf[:], ptr.offset)
		hdr := (*entryHdr)(unsafe.Pointer(&hdrBuf[0]))
		if hdr.expireAt == 0 || hdr.expireAt > now {
			entry := new(Entry)
			entry.Key = make([]byte, hdr.keyLen)
			entry.Value = make([]byte, hdr.valLen)
			entry.ExpireAt = hdr.expireAt
			seg.rb.ReadAt(entry.Key, ptr.offset+ENTRY_HDR_SIZE)
			seg.rb.ReadAt(entry.Value, ptr.offset+ENTRY_HDR_SIZE+int64(hdr.keyLen))
			return entry
		}
	}
	return nil
}

// NewIterator creates a new iterator for the cache.
func (cache *Cache) NewIterator() *Iterator {
	return &Iterator{
		cache: cache,
	}
}
