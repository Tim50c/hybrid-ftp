package rdt

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	barWidth        = 30
	speedWindowDur  = 1200 * time.Millisecond
	renderThrottle  = 100 * time.Millisecond
	spinnerRunes    = "|/-\\"
)

type speedSample struct {
	bytes int64
	at    time.Time
}

type ProgressBar struct {
	total       int64
	current     int64
	startTime   time.Time
	lastUpdate  time.Time

	speedWindow []speedSample
	avgSpeed    float64
	spinnerIdx  int

	mu sync.Mutex
}

func NewProgressBar(total int64) *ProgressBar {
	return &ProgressBar{
		total:      total,
		startTime:  time.Now(),
		lastUpdate: time.Now(),
	}
}

func (p *ProgressBar) Update(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current += int64(n)
	now := time.Now()

	p.speedWindow = append(p.speedWindow, speedSample{int64(n), now})
	p.trimSpeedWindow(now)
	if len(p.speedWindow) > 1 {
		first := p.speedWindow[0]
		last := p.speedWindow[len(p.speedWindow)-1]
		dur := last.at.Sub(first.at)
		if dur > 0 {
			var sum int64
			for _, s := range p.speedWindow {
				sum += s.bytes
			}
			p.avgSpeed = float64(sum) / dur.Seconds()
		}
	}

	if !now.After(p.lastUpdate.Add(renderThrottle)) {
		return
	}
	p.lastUpdate = now

	if p.total == 0 {
		p.spinnerIdx = (p.spinnerIdx + 1) % 4
	}

	p.render()
}

func (p *ProgressBar) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()

	elapsed := time.Since(p.startTime)
	fmt.Printf("\r\033[K")
	if p.total > 0 {
		overall := float64(p.current) / elapsed.Seconds()
		fmt.Printf("Transferred %s in %v (%.1f MiB/s)\n",
			formatSize(p.current), elapsed.Round(time.Millisecond),
			overall/(1<<20))
	} else {
		fmt.Printf("Downloaded %s in %v\n",
			formatSize(p.current), elapsed.Round(time.Millisecond))
	}
}

func (p *ProgressBar) render() {
	speed := formatSpeed(p.avgSpeed)

	if p.total > 0 {
		pct := p.current * 100 / p.total
		filled := int(pct) * barWidth / 100
		bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", barWidth-filled)
		overallSpeed := float64(p.current) / time.Since(p.startTime).Seconds()
		var etaStr string
		if overallSpeed > 0 {
			eta := time.Duration(float64(p.total-p.current)/overallSpeed) * time.Second
			etaStr = fmt.Sprintf("ETA: %02d:%02d", int(eta.Minutes()), int(eta.Seconds())%60)
		} else {
			etaStr = "ETA: --:--"
		}
		fmt.Printf("\r\033[K%s %3d%% | %s | %s", bar, pct, speed, etaStr)
	} else {
		spinner := string(spinnerRunes[p.spinnerIdx])
		fmt.Printf("\r\033[K%s %s downloaded | %s", spinner, formatSize(p.current), speed)
	}
}

func (p *ProgressBar) trimSpeedWindow(now time.Time) {
	cutoff := now.Add(-speedWindowDur)
	i := 0
	for _, s := range p.speedWindow {
		if s.at.After(cutoff) {
			p.speedWindow[i] = s
			i++
		}
	}
	p.speedWindow = p.speedWindow[:i]
}

func formatSpeed(bytesPerSec float64) string {
	switch {
	case bytesPerSec >= 1<<20:
		return fmt.Sprintf("%.1f MiB/s", bytesPerSec/(1<<20))
	case bytesPerSec >= 1<<10:
		return fmt.Sprintf("%.1f KiB/s", bytesPerSec/(1<<10))
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
