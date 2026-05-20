package gesture

import (
	"sync"
	"time"
)

// Debounce provides in-memory cooldown to avoid duplicate gesture triggers.
type Debounce struct {
	mu        sync.Mutex
	last      map[int64]map[string]time.Time
	cooldown  time.Duration
}

func NewDebounce(cooldown time.Duration) *Debounce {
	return &Debounce{
		last:     make(map[int64]map[string]time.Time),
		cooldown: cooldown,
	}
}

func (d *Debounce) InCooldown(telegramID int64, trackID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	byTrack, ok := d.last[telegramID]
	if !ok {
		return false
	}
	t, ok := byTrack[trackID]
	if !ok {
		return false
	}
	return time.Since(t) < d.cooldown
}

func (d *Debounce) Mark(telegramID int64, trackID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last[telegramID] == nil {
		d.last[telegramID] = make(map[string]time.Time)
	}
	d.last[telegramID][trackID] = time.Now()
}
