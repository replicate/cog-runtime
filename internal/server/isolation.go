package server

import (
	"fmt"
	"os/user"
	"strconv"
	"sync"
)

const (
	BaseUID    = 9000
	MaxUID     = 20000
	NoGroupGID = 65534
	NoBodyUID  = 65534
)

type uidCounter struct {
	uid uint32
	mu  sync.Mutex
}

func (u *uidCounter) allocate() (uint32, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	maxAttempts := 1000
	for range maxAttempts {
		u.uid++
		if u.uid < BaseUID || u.uid > MaxUID {
			u.uid = BaseUID
		}
		if _, err := user.LookupId(strconv.FormatUint(uint64(u.uid), 10)); err != nil {
			return u.uid, nil
		}
	}
	// NoBodyUID is used here to ensure we do not accidentally send back root's UID in a posix system
	return NoBodyUID, fmt.Errorf("failed to find unused UID after max attempts")
}
