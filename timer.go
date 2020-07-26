package freecache

import "time"

// timer holds representation of current time.
type Timer interface {
	// Give current time (in seconds)
	Now() uint32
}

type defaultTimer struct{}

func (timer defaultTimer) Now() uint32 {
	return uint32(time.Now().Unix())
}
