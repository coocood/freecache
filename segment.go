package freecache

import (
	"errors"
	"time"
	"unsafe"
)

const HASH_ENTRY_SIZE = 16
const ENTRY_HDR_SIZE = 20

var ErrLargeKey = errors.New("The key is larger than 65535")
var ErrLargeEntry = errors.New("The entry size is larger than 1/1024 of cache size")
var ErrNotFound = errors.New("Entry not found")

// entry pointer struct points to an entry in ring buffer
type entryPtr struct {
	offset   int64  // entry offset in ring buffer
	hash16   uint16 // entries are ordered by hash16 in a slot.
	keyLen   uint16 // used to compare a key
	reserved uint32
}

// entry header struct in ring buffer, followed by key and value.
type entryHdr struct {
	accessTime uint32
	expireAt   uint32
	keyLen     uint16
	hash16     uint16
	valLen     uint32
	deleted    bool
	slotId     uint8
	reserved   uint16
}

// a segment contains 256 slots, a slot is an array of entry pointers ordered by hash16 value
// the entry can be looked up by hash value of the key.
type segment struct {
	rb            RingBuf // ring buffer that stores data
	segId         int
	entryCount    int64
	totalCount    int64      // number of entries in ring buffer, including deleted entries.
	totalTime     int64      // used to calculate least recent used entry.
	totalEvacuate int64      // used for debug
	vacuumLen     int64      // up to vacuumLen, new data can be written without overwriting old data.
	slotLens      [256]int32 // The actual length for every slot.
	slotCap       int32      // max number of entry pointers a slot can hold.
	slotsData     []entryPtr // shared by all 256 slots
}

func newSegment(bufSize int, segId int) (seg segment) {
	seg.rb = NewRingBuf(bufSize, 0)
	seg.segId = segId
	seg.vacuumLen = int64(bufSize)
	seg.slotCap = 1
	seg.slotsData = make([]entryPtr, 256*seg.slotCap)
	return
}

func (seg *segment) set(key, value []byte, hashVal uint64, expireSeconds int) (err error) {
	if len(key) > 65535 {
		return ErrLargeKey
	}
	entryLen := int64(len(key) + len(value) + ENTRY_HDR_SIZE)
	if entryLen > seg.rb.Size()/4 {
		// Do not accept large entry.
		return ErrLargeEntry
	}
	now := uint32(time.Now().Unix())
	expireAt := uint32(0)
	if expireSeconds > 0 {
		expireAt = now + uint32(expireSeconds)
	}

	var hdrBuf [ENTRY_HDR_SIZE]byte
	hdr := (*entryHdr)(unsafe.Pointer(&hdrBuf[0]))
	hdr.accessTime = now
	hdr.expireAt = expireAt
	hdr.keyLen = uint16(len(key))
	hdr.hash16 = uint16(hashVal >> 16)
	hdr.valLen = uint32(len(value))
	hdr.slotId = uint8(hashVal >> 8)

	var oldHdrBuf [ENTRY_HDR_SIZE]byte
	consecutiveEvacuate := 0
	for seg.vacuumLen < entryLen {
		oldOff := seg.rb.End() + seg.vacuumLen - seg.rb.Size()
		seg.rb.ReadAt(oldHdrBuf[:], oldOff)
		oldHdr := (*entryHdr)(unsafe.Pointer(&oldHdrBuf[0]))
		oldEntryLen := int64(oldHdr.keyLen) + int64(oldHdr.valLen) + ENTRY_HDR_SIZE
		if oldHdr.deleted {
			consecutiveEvacuate = 0
			seg.totalTime -= int64(oldHdr.accessTime)
			seg.totalCount--
			seg.vacuumLen += oldEntryLen
			continue
		}
		expired := oldHdr.expireAt != 0 && oldHdr.expireAt < now
		leastRecentUsed := int64(oldHdr.accessTime)*seg.totalCount <= seg.totalTime
		if expired || leastRecentUsed || consecutiveEvacuate > 5 {
			seg.delEntryPtr(oldHdr.slotId, oldHdr.hash16, oldOff)
			consecutiveEvacuate = 0
			seg.totalTime -= int64(oldHdr.accessTime)
			seg.totalCount--
			seg.vacuumLen += oldEntryLen
		} else {
			// evacuate an old entry that has been accessed recently for better cache hit rate.
			newOff := seg.rb.Evacuate(oldOff, int(oldEntryLen))
			seg.updateEntryPtr(oldOff, newOff, oldHdr.hash16, oldHdr.slotId)
			consecutiveEvacuate++
			seg.totalEvacuate++
		}
	}

	off := seg.rb.End()
	seg.rb.Write(hdrBuf[:])
	seg.rb.Write(key)
	seg.rb.Write(value)
	seg.totalTime += int64(now)
	seg.totalCount++
	seg.vacuumLen -= entryLen
	seg.setEntryPtr(key, off, hdr.hash16, hdr.slotId)
	return
}

func (seg *segment) get(key []byte, hashVal uint64) (value []byte, err error) {
	slotId := uint8(hashVal >> 8)
	hash16 := uint16(hashVal >> 16)
	slotOff := int32(slotId) * seg.slotCap
	var slot = seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	idx, match := seg.lookup(slot, hash16, key)
	if !match {
		err = ErrNotFound
		return
	}
	ptr := &slot[idx]
	now := uint32(time.Now().Unix())

	var hdrBuf [ENTRY_HDR_SIZE]byte
	seg.rb.ReadAt(hdrBuf[:], ptr.offset)
	hdr := (*entryHdr)(unsafe.Pointer(&hdrBuf[0]))

	if hdr.expireAt != 0 && hdr.expireAt <= now {
		seg.delEntryPtr(slotId, hash16, ptr.offset)
		err = ErrNotFound
		return
	}
	seg.totalTime += int64(now - hdr.accessTime)
	hdr.accessTime = now
	seg.rb.WriteAt(hdrBuf[:], ptr.offset)
	value = make([]byte, hdr.valLen)
	seg.rb.ReadAt(value, ptr.offset+ENTRY_HDR_SIZE+int64(hdr.keyLen))
	return
}

func (seg *segment) del(key []byte, hashVal uint64) (affected bool) {
	slotId := uint8(hashVal >> 8)
	hash16 := uint16(hashVal >> 16)
	slotOff := int32(slotId) * seg.slotCap
	slot := seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	idx, match := seg.lookup(slot, hash16, key)
	if !match {
		return false
	}
	ptr := &slot[idx]
	seg.delEntryPtr(slotId, hash16, ptr.offset)
	return true
}

func (seg *segment) expand() {
	newSlotData := make([]entryPtr, seg.slotCap*2*256)
	for i := 0; i < 256; i++ {
		off := int32(i) * seg.slotCap
		copy(newSlotData[off*2:], seg.slotsData[off:off+seg.slotLens[i]])
	}
	seg.slotCap *= 2
	seg.slotsData = newSlotData
}

func (seg *segment) updateEntryPtr(oldOff, newOff int64, hash16 uint16, slotId uint8) {
	slotOff := int32(slotId) * seg.slotCap
	slot := seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	idx, match := seg.lookupByOff(slot, hash16, oldOff)
	if !match {
		return
	}
	ptr := &slot[idx]
	ptr.offset = newOff
}

func (seg *segment) setEntryPtr(key []byte, offset int64, hash16 uint16, slotId uint8) {
	var ptr entryPtr
	ptr.offset = offset
	ptr.hash16 = hash16
	ptr.keyLen = uint16(len(key))
	slotOff := int32(slotId) * seg.slotCap
	slot := seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	idx, match := seg.lookupByOff(slot, hash16, offset)
	if match {
		oldPtr := &slot[idx]
		var oldEntryHdrBuf [ENTRY_HDR_SIZE]byte
		seg.rb.ReadAt(oldEntryHdrBuf[:], oldPtr.offset)
		oldEntryHdr := (*entryHdr)(unsafe.Pointer(&oldEntryHdrBuf[0]))
		oldEntryHdr.deleted = true
		seg.rb.WriteAt(oldEntryHdrBuf[:], oldPtr.offset)
		// update entry pointer
		oldPtr.offset = offset
		return
	}
	// insert new entry
	if len(slot) == cap(slot) {
		seg.expand()
		slotOff *= 2
	}
	seg.slotLens[slotId]++
	seg.entryCount++
	slot = seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	copy(slot[idx+1:], slot[idx:])
	slot[idx] = ptr
}

func (seg *segment) delEntryPtr(slotId uint8, hash16 uint16, offset int64) {
	slotOff := int32(slotId) * seg.slotCap
	slot := seg.slotsData[slotOff : slotOff+seg.slotLens[slotId] : slotOff+seg.slotCap]
	idx, match := seg.lookupByOff(slot, hash16, offset)
	if !match {
		return
	}
	var entryHdrBuf [ENTRY_HDR_SIZE]byte
	seg.rb.ReadAt(entryHdrBuf[:], offset)
	entryHdr := (*entryHdr)(unsafe.Pointer(&entryHdrBuf[0]))
	entryHdr.deleted = true
	seg.rb.WriteAt(entryHdrBuf[:], offset)
	copy(slot[idx:], slot[idx+1:])
	seg.slotLens[slotId]--
	seg.entryCount--
}

func entryPtrIdx(slot []entryPtr, hash16 uint16) (idx int) {
	high := len(slot)
	for idx < high {
		mid := (idx + high) >> 1
		oldEntry := &slot[mid]
		if oldEntry.hash16 < hash16 {
			idx = mid + 1
		} else {
			high = mid
		}
	}
	return
}

func (seg *segment) lookup(slot []entryPtr, hash16 uint16, key []byte) (idx int, match bool) {
	idx = entryPtrIdx(slot, hash16)
	for idx < len(slot) {
		ptr := &slot[idx]
		if ptr.hash16 != hash16 {
			break
		}
		match = int(ptr.keyLen) == len(key) && seg.rb.EqualAt(key, ptr.offset+ENTRY_HDR_SIZE)
		if match {
			return
		}
		idx++
	}
	return
}

func (seg *segment) lookupByOff(slot []entryPtr, hash16 uint16, offset int64) (idx int, match bool) {
	idx = entryPtrIdx(slot, hash16)
	for idx < len(slot) {
		ptr := &slot[idx]
		if ptr.hash16 != hash16 {
			break
		}
		match = ptr.offset == offset
		if match {
			return
		}
		idx++
	}
	return
}
