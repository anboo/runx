package process

import (
	"time"
)

// CollectMetrics periodically refreshes the process's metrics snapshot.
// Values are aggregated over the whole process tree (leader + children), so a
// wrapper like `go run .` reports the load of its real child binary instead
// of the (idle) wrapper.
//
// Only per-process numbers are collected. Host-wide counters (interface
// traffic, global IO wait) are intentionally not used - they describe the
// machine, not the process.
func CollectMetrics(p *ManagedProcess) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.RLock()
		pid := p.PID
		status := p.Status
		p.mu.RUnlock()

		if status != StatusRunning {
			entry := MetricsSnapshot{Timestamp: time.Now().UnixMilli()}
			p.mu.Lock()
			p.Metrics = &entry
			p.mu.Unlock()
			continue
		}

		tree, err := buildTree(pid)
		if err != nil {
			continue
		}
		tm := treeMetrics(tree)

		entry := MetricsSnapshot{
			CPU:              tm.CPU,
			Memory:           tm.RSS,
			RSS:              tm.RSS,
			VirtualMemory:    tm.VMS,
			Threads:          tm.Threads,
			FDCount:          tm.FDs,
			DiskRead:         tm.ReadBytes,
			DiskWrite:        tm.WriteBytes,
			ContextSwitches:  tm.CtxSwitches,
			Timestamp:        time.Now().UnixMilli(),
		}

		p.mu.Lock()
		p.Metrics = &entry
		p.mu.Unlock()
	}
}