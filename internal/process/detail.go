package process

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	psnet "github.com/shirou/gopsutil/v3/net"
	goprocess "github.com/shirou/gopsutil/v3/process"
)

// ProcNode is one entry of the process tree rooted at the managed process.
type ProcNode struct {
	PID   int    `json:"pid"`
	PPID  int    `json:"ppid"`
	CMD   string `json:"cmd"`
	State string `json:"state"`
}

// PortInfo is a listening socket owned by the process tree.
type PortInfo struct {
	Proto string `json:"proto"` // tcp, tcp6, udp, udp6, unix
	Addr  string `json:"addr"`
	Port  int    `json:"port"`
}

// ConnInfo is a grouped outbound/inbound socket of the process tree.
type ConnInfo struct {
	Proto  string `json:"proto"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
	State  string `json:"state"`
	Count  int    `json:"count"`
}

// TreeMetrics aggregates resource usage over the whole process tree, so a
// wrapper like `go run .` reports the load of its real child binary.
type TreeMetrics struct {
	CPU          float64 `json:"cpu"`
	RSS          uint64  `json:"rss"`
	VMS          uint64  `json:"vms"`
	Swap         uint64  `json:"swap"`
	Threads      int32   `json:"threads"`
	FDs          int     `json:"fd_count"`
	ReadBytes    uint64  `json:"read_bytes"`
	WriteBytes   uint64  `json:"write_bytes"`
	UserTime     float64 `json:"user_time"`
	SystemTime   float64 `json:"system_time"`
	CtxSwitches  int64   `json:"context_switches"`
}

// ProcessDetail is a full on-demand snapshot of a managed process: its
// process tree, listening ports, network connections, open file paths,
// executable/cwd and tree-aggregated metrics.
type ProcessDetail struct {
	ProcessID   string       `json:"process_id"`
	Tree        []ProcNode   `json:"tree"`
	Ports       []PortInfo   `json:"ports"`
	Connections []ConnInfo   `json:"connections"`
	FDPaths     []string     `json:"fd_paths,omitempty"`
	Exe         string       `json:"exe,omitempty"`
	CWD         string       `json:"cwd,omitempty"`
	Metrics     TreeMetrics  `json:"metrics"`
}

// Detail returns an on-demand snapshot for a managed process. It walks the
// whole process tree (leader + children) so wrappers like go run or sh -c
// expose their real workload.
func (m *Manager) Detail(id string) (*ProcessDetail, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}

	p.mu.RLock()
	leader := p.PID
	p.mu.RUnlock()

	tree, err := buildTree(leader)
	if err != nil {
		return nil, err
	}

	detail := &ProcessDetail{ProcessID: id, Tree: tree}

	// Leader's exe/cwd come from the process info we already have.
	if len(tree) > 0 {
		if exe, err := goprocess.NewProcess(int32(tree[0].PID)); err == nil {
			if e, err := exe.Exe(); err == nil {
				detail.Exe = e
			}
			if c, err := exe.Cwd(); err == nil {
				detail.CWD = c
			}
		}
	}

	detail.Metrics = treeMetrics(tree)
	detail.Ports, detail.Connections = networkInfo(tree)
	detail.FDPaths = fdPaths(tree)

	return detail, nil
}

// buildTree returns the process tree rooted at leader, children first,
// using one pass over the process table.
func buildTree(leader int) ([]ProcNode, error) {
	procs, err := goprocess.Processes()
	if err != nil {
		return nil, fmt.Errorf("process table: %w", err)
	}

	byPID := make(map[int]*ProcNode, len(procs))
	children := make(map[int][]int)
	for _, pr := range procs {
		pid := int(pr.Pid)
		ppid := 0
		if p, err := pr.Ppid(); err == nil {
			ppid = int(p)
		}
		state := ""
		if s, err := pr.Status(); err == nil && len(s) > 0 {
			state = s[0]
		}
		if state == "" {
			state = "?"
		}
		cmd := ""
		if c, err := pr.Cmdline(); err == nil {
			cmd = c
		}
		if cmd == "" {
			if n, err := pr.Name(); err == nil {
				cmd = n
			}
		}
		byPID[pid] = &ProcNode{PID: pid, PPID: ppid, CMD: cmd, State: state}
		children[ppid] = append(children[ppid], pid)
	}

	// BFS from the leader.
	var order []int
	queue := []int{leader}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		order = append(order, pid)
		queue = append(queue, children[pid]...)
	}

	tree := make([]ProcNode, 0, len(order))
	for _, pid := range order {
		if n, ok := byPID[pid]; ok {
			tree = append(tree, *n)
		}
	}
	return tree, nil
}

// treeMetrics sums per-process resource usage over the tree.
func treeMetrics(tree []ProcNode) TreeMetrics {
	var tm TreeMetrics
	for _, n := range tree {
		pr, err := goprocess.NewProcess(int32(n.PID))
		if err != nil {
			continue
		}
		if cpu, err := pr.CPUPercent(); err == nil {
			tm.CPU += cpu
		}
		if mi, err := pr.MemoryInfo(); err == nil {
			tm.RSS += mi.RSS
			tm.VMS += mi.VMS
			tm.Swap += mi.Swap
		}
		if th, err := pr.NumThreads(); err == nil {
			tm.Threads += th
		}
		if fds, err := pr.NumFDs(); err == nil {
			tm.FDs += int(fds)
		}
		if io, err := pr.IOCounters(); err == nil {
			tm.ReadBytes += io.ReadBytes
			tm.WriteBytes += io.WriteBytes
		}
		if ct, err := pr.Times(); err == nil {
			tm.UserTime += ct.User
			tm.SystemTime += ct.System
		}
		if cs, err := pr.NumCtxSwitches(); err == nil {
			tm.CtxSwitches += int64(cs.Voluntary + cs.Involuntary)
		}
	}
	return tm
}

// networkInfo splits the process tree's sockets into listening ports and
// grouped connections. Connections are keyed by proto/local/remote/state so
// an agent sees "12 ESTABLISHED to 1.2.3.4:443" instead of 12 rows.
func networkInfo(tree []ProcNode) ([]PortInfo, []ConnInfo) {
	pidSet := make(map[int]bool, len(tree))
	for _, n := range tree {
		pidSet[n.PID] = true
	}

	var ports []PortInfo
	connGroups := make(map[string]*ConnInfo)
	var connOrder []string

	conns, err := psnet.Connections("all")
	if err != nil {
		return ports, nil
	}

	for _, c := range conns {
		if !pidSet[int(c.Pid)] {
			continue
		}
		proto := protoName(c)

		if c.Status == "LISTEN" {
			ports = append(ports, PortInfo{
				Proto: proto,
				Addr:  c.Laddr.IP,
				Port:  int(c.Laddr.Port),
			})
			continue
		}

		key := fmt.Sprintf("%s|%s:%d|%s:%d|%s", proto, c.Laddr.IP, c.Laddr.Port, c.Raddr.IP, c.Raddr.Port, c.Status)
		if g, ok := connGroups[key]; ok {
			g.Count++
			continue
		}
		connGroups[key] = &ConnInfo{
			Proto:  proto,
			Local:  fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
			Remote: fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
			State:  c.Status,
			Count:  1,
		}
		connOrder = append(connOrder, key)
	}

	connsOut := make([]ConnInfo, 0, len(connOrder))
	for _, k := range connOrder {
		connsOut = append(connsOut, *connGroups[k])
	}

	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		return ports[i].Proto < ports[j].Proto
	})
	sort.Slice(connsOut, func(i, j int) bool {
		return connsOut[i].Remote < connsOut[j].Remote
	})

	return ports, connsOut
}

// protoName derives a readable protocol name from the socket family and type.
func protoName(c psnet.ConnectionStat) string {
	switch c.Family {
	case syscall.AF_UNIX:
		return "unix"
	case syscall.AF_INET6:
		if c.Type == syscall.SOCK_DGRAM {
			return "udp6"
		}
		return "tcp6"
	default:
		if c.Type == syscall.SOCK_DGRAM {
			return "udp"
		}
		return "tcp"
	}
}

// fdPaths resolves open file descriptors of the process tree to their target
// paths. Only available on platforms with a /proc filesystem; elsewhere it
// returns nil.
func fdPaths(tree []ProcNode) []string {
	return fdPathsImpl(tree)
}

// fdPathMax caps the number of collected fd paths per snapshot.
const fdPathMax = 200

// procFDPaths lists readable fd targets for one pid via /proc/<pid>/fd.
func procFDPaths(pid int) []string {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, target)
	}
	return out
}