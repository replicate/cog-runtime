package server

import (
	"errors"
	"math"
	"os/user"
	"strconv"
	"sync/atomic"
)

const (
	BaseUID    = 9000
	NoGroupGID = 65534
	NoBodyUID  = 65534
)

type uidCounter struct {
	atomic.Int64
}

func newUIDCounter() *uidCounter {
	counter := &uidCounter{}
	counter.Store(BaseUID - 1) // -1 to ensure we start at BaseUID
	return counter
}

func (u *uidCounter) allocate() (int, error) {
	maxAttempts := 1000
	for range maxAttempts {
		nextUID := u.Add(1)

		// Use modulo to loop aroundensure we don't exceed uint32 max
		uid := int(uint32(nextUID) % math.MaxUint32)

		if _, err := user.LookupId(strconv.Itoa(uid)); err != nil {
			return uid, nil
		}
	}
	// NoBodyUID is used here to ensure we do not accidently send back root's UID in a posix system
	return NoBodyUID, errors.New("failed to find unused UID after max attempts")
}
