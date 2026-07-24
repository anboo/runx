package process

import (
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

func CollectMetrics(p *ManagedProcess) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var prevNet *net.IOCountersStat

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

		proc, err := process.NewProcess(int32(pid))
		if err != nil {
			continue
		}

		cpuPercent, _ := proc.CPUPercent()
		memInfo, _ := proc.MemoryInfo()
		memPercent, _ := proc.MemoryPercent()
		threads, _ := proc.NumThreads()
		fds, _ := proc.NumFDs()
		ioCounters, _ := proc.IOCounters()
		ctxSwitches, _ := proc.NumCtxSwitches()

		netIO, _ := net.IOCounters(false)
		var netRX, netTX uint64
		if len(netIO) > 0 {
			if prevNet != nil {
				netRX = netIO[0].BytesRecv - prevNet.BytesRecv
				netTX = netIO[0].BytesSent - prevNet.BytesSent
			}
			prevNet = &netIO[0]
		}

		entry := MetricsSnapshot{
			CPU:             cpuPercent,
			Memory:          uint64(memPercent * 1024 * 1024 / 100),
			Timestamp:       time.Now().UnixMilli(),
		}

		if memInfo != nil {
			entry.RSS = memInfo.RSS
			entry.VirtualMemory = memInfo.VMS
		}

		entry.Threads = threads

		entry.FDCount = int(fds)

		if ioCounters != nil {
			entry.DiskRead = ioCounters.ReadBytes
			entry.DiskWrite = ioCounters.WriteBytes
		}

		if ctxSwitches != nil {
			entry.ContextSwitches = int64(ctxSwitches.Voluntary + ctxSwitches.Involuntary)
		}

		entry.NetworkRX = netRX
		entry.NetworkTX = netTX

		cpuTimes, _ := cpu.Times(false)
		if len(cpuTimes) > 0 {
			total := cpuTimes[0].User + cpuTimes[0].System + cpuTimes[0].Idle + cpuTimes[0].Iowait
			if total > 0 {
				entry.IOWait = (cpuTimes[0].Iowait / total) * 100
			}
		}

		p.mu.Lock()
		p.Metrics = &entry
		p.mu.Unlock()
	}
}
