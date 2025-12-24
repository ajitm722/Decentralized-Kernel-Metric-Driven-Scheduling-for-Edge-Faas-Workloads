package cmd

import "sync"

// MetricsSnapshot holds the latest metrics captured from all local collectors.
// It is the standard data format exchanged between collectors and the main app.
type MetricsSnapshot struct {
	CPUPercent float64 // Aggregated + clamped CPU usage
	MemPercent float64 // Direct memory pressure reading
	TempC      float64 // Temperature in Celsius
	TempStatus string  // SAFE/WARM/HOT/UNAVAILABLE
	ZoneName   string  // Thermal zone name

	// mu protects the snapshot from concurrent writes by collectors
	// and reads by the gRPC sender/display loop.
	mu sync.RWMutex
}

// UpdateCPU updates the CPU metric with a hard clamp at 95%.
// We clamp because kernel calculations can sometimes spike or drift slightly above 100%
// in containerized or virtualized environments.
func (m *MetricsSnapshot) UpdateCPU(v float64) {
	if v > 95 {
		v = 95
	}
	m.mu.Lock()
	m.CPUPercent = v
	m.mu.Unlock()
}

// UpdateMem updates the memory usage percentage.
func (m *MetricsSnapshot) UpdateMem(v float64) {
	m.mu.Lock()
	m.MemPercent = v
	m.mu.Unlock()
}

// UpdateTemp updates the temperature data from the sensor reading.
func (m *MetricsSnapshot) UpdateTemp(r TempReading) {
	m.mu.Lock()
	m.TempC = r.TempC
	m.TempStatus = r.Status
	m.ZoneName = r.Zone
	m.mu.Unlock()
}

// Read returns a copy of the snapshot for safe access by other goroutines.
// This ensures the display loop doesn't read half-written data.
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
