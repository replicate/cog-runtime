package util

import (
	"net"
	"sync"

	"github.com/replicate/go/must"
)

var (
	ports   = make(map[int]bool)
	portsMu sync.Mutex
)

type PortFinder struct {
	ports map[int]bool
	mu    sync.Mutex
}

func FindPort() int {
	portsMu.Lock()
	defer portsMu.Unlock()
	for {
		a := must.Get(net.ResolveTCPAddr("tcp", "localhost:0"))
		l := must.Get(net.ListenTCP("tcp", a))
		p := l.Addr().(*net.TCPAddr).Port
		if _, ok := ports[p]; !ok {
			ports[p] = true
			l.Close()
			return p
		}
	}
}
