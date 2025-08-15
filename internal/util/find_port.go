package util

import (
	"net"
	"sync"
)

var (
	ports   = make(map[int]bool)
	portsMu sync.Mutex
)

type PortFinder struct {
	ports map[int]bool
	mu    sync.Mutex
}

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
		p := l.Addr().(*net.TCPAddr).Port
		if _, ok := ports[p]; !ok {
			ports[p] = true
			l.Close()
			return p, nil
		}
	}
}
