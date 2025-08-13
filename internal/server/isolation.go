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
)

var uidCounter int64 = BaseUID

func allocateUID() (int, error) {
	maxAttempts := 1000
	for range maxAttempts {
		nextUID := atomic.AddInt64(&uidCounter, 1) - 1

		// Use modulo to loop aroundensure we don't exceed uint32 max
		uid := int(uint32(nextUID) % math.MaxUint32)

		if _, err := user.LookupId(strconv.Itoa(uid)); err != nil {
			return uid, nil
		}
	}
	return 0, errors.New("failed to find unused UID after max attempts")
}

func resetUIDCounter() {
	atomic.StoreInt64(&uidCounter, BaseUID)
}
