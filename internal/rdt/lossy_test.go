package rdt

import (
	"math/rand"
	"net"
	"sync"
	"time"
)

type LossyConfig struct {
	DropRate    float64
	DelayMin    time.Duration
	DelayMax    time.Duration
	ReorderProb float64
	CorruptRate float64
	Seed        int64
}

type heldPacket struct {
	data []byte
	addr *net.UDPAddr
	n    int
}

type LossyUDPConn struct {
	*net.UDPConn
	cfg  LossyConfig
	rng  *rand.Rand
	mu   sync.Mutex
	held *heldPacket
}

func NewLossyUDPConn(conn *net.UDPConn, cfg LossyConfig) *LossyUDPConn {
	return &LossyUDPConn{
		UDPConn: conn,
		cfg:     cfg,
		rng:     rand.New(rand.NewSource(cfg.Seed)),
	}
}

func (c *LossyUDPConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	for {
		c.mu.Lock()
		if c.held != nil && c.rng.Float64() < c.cfg.ReorderProb {
			h := c.held
			c.held = nil
			c.mu.Unlock()
			copy(b, h.data[:h.n])
			return h.n, h.addr, nil
		}
		c.mu.Unlock()

		n, addr, err := c.UDPConn.ReadFromUDP(b)
		if err != nil {
			return n, addr, err
		}

		if c.rng.Float64() < c.cfg.DropRate {
			continue
		}

		if c.rng.Float64() < c.cfg.CorruptRate && n > 0 {
			idx := c.rng.Intn(n)
			b[idx] ^= 0xFF
		}

		if c.cfg.DelayMax > 0 {
			delay := c.cfg.DelayMin
			if c.cfg.DelayMax > c.cfg.DelayMin {
				delay += time.Duration(c.rng.Int63n(int64(c.cfg.DelayMax - c.cfg.DelayMin)))
			}
			time.Sleep(delay)
		}

		if c.rng.Float64() < c.cfg.ReorderProb {
			c.mu.Lock()
			saved := make([]byte, n)
			copy(saved, b[:n])
			c.held = &heldPacket{data: saved, addr: addr, n: n}
			c.mu.Unlock()
			continue
		}

		return n, addr, nil
	}
}
