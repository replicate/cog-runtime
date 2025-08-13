package server

import (
	"errors"
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
	atomic.Uint32
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
		if nextUID < BaseUID {
			// This may cause us to skip some UIDs but that is fine, and it is unlikely to happen
			// since we would have had to run >4MM iterations of this loop
			nextUID = u.Add(BaseUID)
		}

		uid := int(nextUID)
		if _, err := user.LookupId(strconv.Itoa(uid)); err != nil {
			return uid, nil
		}
	}
	// NoBodyUID is used here to ensure we do not accidently send back root's UID in a posix system
	return NoBodyUID, errors.New("failed to find unused UID after max attempts")
}
