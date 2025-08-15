package util //nolint:revive // TODO: this is not a meaningful package name, move functionality to where it's used

import (
	"fmt"
	"net"
	"sync"
)

var (
	ports   = make(map[int]bool)
	portsMu sync.Mutex
)

func FindPort() (int, error) {
	portsMu.Lock()
	defer portsMu.Unlock()
	for {
		a, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return 0, err
		}
		l, err := net.ListenTCP("tcp", a)
		if err != nil {
			return 0, err
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			return 0, fmt.Errorf("failed to get port from TCP address: %w", err)
		}
		p := addr.Port
		if _, ok := ports[p]; !ok {
			ports[p] = true
			_ = l.Close()
			return p, nil
		}
	}
}
