package cmd

import "sync"

// MetricsSnapshot holds the latest metrics captured from all collectors.
// It is shared between multiple goroutines, so we protect it with a RWMutex.
type MetricsSnapshot struct {
	CPUPercent float64 // Aggregated + clamped CPU usage
	MemPercent float64 // Direct memory pressure reading
	TempC      float64 // Temperature in Celsius
	TempStatus string  // SAFE/WARM/HOT/UNAVAILABLE
	ZoneName   string  // Thermal zone name

	mu sync.RWMutex
}

// --- CPU Update (with hard clamp) ---
// CPU may overshoot due to kernel behaviour, VMs, timer drift, etc.
// Instead of smoothing, we simply clamp to 95%.

func (m *MetricsSnapshot) UpdateCPU(v float64) {
	if v > 95 {
		v = 95 // hard clamp
	}
	m.mu.Lock()
	m.CPUPercent = v
	m.mu.Unlock()
}

func (m *MetricsSnapshot) UpdateMem(v float64) {
	m.mu.Lock()
	m.MemPercent = v
	m.mu.Unlock()
}

func (m *MetricsSnapshot) UpdateTemp(r TempReading) {
	m.mu.Lock()
	m.TempC = r.TempC
	m.TempStatus = r.Status
	m.ZoneName = r.Zone
	m.mu.Unlock()
}

// Read returns a copy of the snapshot for safe access by other goroutines.
func (m *MetricsSnapshot) Read() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MetricsSnapshot{
		CPUPercent: m.CPUPercent,
		MemPercent: m.MemPercent,
		TempC:      m.TempC,
		TempStatus: m.TempStatus,
		ZoneName:   m.ZoneName,
	}
}
