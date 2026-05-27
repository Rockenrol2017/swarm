//go:build !linux

package swarmnode

// bandwidth_other.go — заглушка BandwidthMonitor для не-Linux платформ.
// На Windows/macOS/BSD /proc/net/dev не существует — монитор не запускается.
// Все методы возвращают нулевые значения (relay всегда принимается).

import "context"

// BandwidthMonitor — заглушка для не-Linux.
type BandwidthMonitor struct{}

// newBandwidthMonitor создаёт заглушку.
func newBandwidthMonitor() *BandwidthMonitor {
	return &BandwidthMonitor{}
}

// start — нет-оп на не-Linux.
func (bw *BandwidthMonitor) start(_ context.Context) {}

// CurrentLoadPercent всегда возвращает 0 (канал считается свободным).
func (bw *BandwidthMonitor) CurrentLoadPercent() float64 { return 0 }

// Stats возвращает нули.
func (bw *BandwidthMonitor) Stats() (rxMbps, txMbps, loadPct float64) { return 0, 0, 0 }
