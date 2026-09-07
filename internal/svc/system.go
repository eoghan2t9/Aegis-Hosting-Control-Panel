package svc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"aegis/internal/config"
)

// System reports host resource usage. Per-user CPU/memory accounting reads
// /proc directly (cheap, precise); server-wide metrics come from gopsutil.
type System struct {
	Cfg       *config.Config
	StartTime time.Time
	cores     int
}

func NewSystem(cfg *config.Config) *System {
	cores, _ := cpu.Counts(true)
	return &System{Cfg: cfg, StartTime: time.Now(), cores: cores}
}

// ServiceStatus describes one host service's state.
type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // active | inactive | missing
}

// Overview is the dashboard's system summary.
type Overview struct {
	Hostname    string          `json:"hostname"`
	OS          string          `json:"os"`
	Kernel      string          `json:"kernel"`
	Arch        string          `json:"arch"`
	UptimeSecs  uint64          `json:"uptime_secs"`
	PanelUptime int64           `json:"panel_uptime_secs"`
	CPUCores    int             `json:"cpu_cores"`
	CPUModel    string          `json:"cpu_model"`
	Load        []float64       `json:"load"`
	MemTotal    uint64          `json:"mem_total"`
	MemUsed     uint64          `json:"mem_used"`
	SwapTotal   uint64          `json:"swap_total"`
	SwapUsed    uint64          `json:"swap_used"`
	Disks       []DiskInfo      `json:"disks"`
	Services    []ServiceStatus `json:"services"`
}

type DiskInfo struct {
	Mount string  `json:"mount"`
	Total uint64  `json:"total"`
	Used  uint64  `json:"used"`
	Pct   float64 `json:"pct"`
}

func (s *System) Overview() Overview {
	ov := Overview{
		Services: []ServiceStatus{
			{Name: "nginx", Status: statusOf("nginx")},
			{Name: "apache2", Status: statusOf("apache2")},
			{Name: "mariadb", Status: statusOf("mariadb")},
			{Name: "postgresql", Status: statusOf("postgresql")},
			{Name: "vsftpd", Status: statusOf("vsftpd")},
		},
		PanelUptime: int64(time.Since(s.StartTime).Seconds()),
	}
	if hi, err := host.Info(); err == nil {
		ov.Hostname = hi.Hostname
		ov.OS = hi.Platform + " " + hi.PlatformVersion
		ov.Kernel = hi.KernelVersion
		ov.Arch = hi.KernelArch
		ov.UptimeSecs = hi.Uptime
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		ov.CPUModel = ci[0].ModelName
	}
	ov.CPUCores = s.cores
	if l, err := load.Avg(); err == nil {
		ov.Load = []float64{l.Load1, l.Load5, l.Load15}
	}
	if m, err := mem.VirtualMemory(); err == nil {
		ov.MemTotal, ov.MemUsed = m.Total, m.Used
	}
	if sw, err := mem.SwapMemory(); err == nil {
		ov.SwapTotal, ov.SwapUsed = sw.Total, sw.Used
	}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if strings.HasPrefix(p.Mountpoint, "/snap") || strings.HasPrefix(p.Mountpoint, "/boot") {
				continue
			}
			u, err := disk.Usage(p.Mountpoint)
			if err != nil || u.Total == 0 {
				continue
			}
			ov.Disks = append(ov.Disks, DiskInfo{Mount: p.Mountpoint, Total: u.Total, Used: u.Used, Pct: u.UsedPercent})
		}
	}
	// Ensure the home root always appears.
	found := false
	for _, d := range ov.Disks {
		if d.Mount == s.Cfg.HomeRoot {
			found = true
		}
	}
	if !found {
		if u, err := disk.Usage(s.Cfg.HomeRoot); err == nil {
			ov.Disks = append(ov.Disks, DiskInfo{Mount: s.Cfg.HomeRoot, Total: u.Total, Used: u.Used, Pct: u.UsedPercent})
		}
	}
	return ov
}

func statusOf(service string) string {
	if !LookPath("systemctl") {
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, _ := Exec(ctx, "systemctl", "is-active", service)
	switch out {
	case "active":
		return "active"
	case "inactive", "failed", "unknown":
		return out
	default:
		return "inactive"
	}
}

// --- per-user accounting -------------------------------------------------------

// UserUsage aggregates resource use per system uid.
type UserUsage struct {
	Username   string  `json:"username"`
	UID        int     `json:"uid"`
	CPUSeconds float64 `json:"cpu_seconds"`
	RSSBytes   int64   `json:"rss_bytes"`
	DiskBytes  int64   `json:"disk_bytes"`
}

// PerUserUsage reads /proc once and groups CPU + RSS by uid, then adds disk
// usage from the home root. usernames maps uid -> panel username.
func (s *System) PerUserUsage(usernames map[int]string) ([]UserUsage, error) {
	cpuByUID := map[int]float64{}
	rssByUID := map[int]int64{}
	pageSize := int64(os.Getpagesize())

	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	for _, pe := range procs {
		if _, err := strconv.Atoi(pe.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", pe.Name(), "stat"))
		if err != nil {
			continue
		}
		// comm may contain spaces/parens; parse from the last ')'.
		line := string(stat)
		closeIdx := strings.LastIndexByte(line, ')')
		if closeIdx < 0 || closeIdx+2 >= len(line) {
			continue
		}
		fields := strings.Fields(line[closeIdx+2:])
		if len(fields) < 22 {
			continue
		}
		// fields after comm: state(0) ppid(1) ... utime(11) stime(12) ... rss(21)
		utime, _ := strconv.ParseFloat(fields[11], 64)
		stime, _ := strconv.ParseFloat(fields[12], 64)
		rssPages, _ := strconv.ParseInt(fields[21], 10, 64)
		status, err := os.ReadFile(filepath.Join("/proc", pe.Name(), "status"))
		if err != nil {
			continue
		}
		uid := 0
		for _, l := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(l, "Uid:") {
				f := strings.Fields(l)
				if len(f) >= 2 {
					uid, _ = strconv.Atoi(f[1])
				}
				break
			}
		}
		cpuByUID[uid] += (utime + stime) / 100.0 // clock ticks -> seconds
		rssByUID[uid] += rssPages * pageSize
	}

	// Disk usage per user home.
	diskByUser := map[string]int64{}
	entries, err := os.ReadDir(s.Cfg.HomeRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(s.Cfg.HomeRoot, e.Name())
			if u, err := diskUsage(path); err == nil {
				diskByUser[e.Name()] = u
			}
		}
	}

	var out []UserUsage
	for uid, uname := range usernames {
		uu := UserUsage{Username: uname, UID: uid, CPUSeconds: cpuByUID[uid], RSSBytes: rssByUID[uid], DiskBytes: diskByUser[uname]}
		out = append(out, uu)
	}
	// sort by username for stable output
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Username < out[j-1].Username; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// UIDFor returns the system uid for a username (best-effort).
func UIDFor(username string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := Exec(ctx, "id", "-u", username)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// diskUsage computes the size of a path via du -sb (fast, honors hardlinks).
func diskUsage(path string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := Exec(ctx, "du", "-sb", path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, fmt.Errorf("du: empty output")
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// --- processes -----------------------------------------------------------------

type Process struct {
	PID      int     `json:"pid"`
	UID      int     `json:"uid"`
	User     string  `json:"user"`
	Name     string  `json:"name"`
	CPU      float64 `json:"cpu"` // seconds
	RSSBytes int64   `json:"rss_bytes"`
	Command  string  `json:"command"`
}

// Processes lists /proc processes, optionally filtered by uid (0 = all).
func (s *System) Processes(uidFilter int) ([]Process, error) {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, pe := range procs {
		pid, err := strconv.Atoi(pe.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", pe.Name(), "stat"))
		if err != nil {
			continue
		}
		line := string(stat)
		closeIdx := strings.LastIndexByte(line, ')')
		if closeIdx < 0 {
			continue
		}
		name := line[strings.IndexByte(line, '(')+1 : closeIdx]
		fields := strings.Fields(line[closeIdx+2:])
		if len(fields) < 22 {
			continue
		}
		utime, _ := strconv.ParseFloat(fields[11], 64)
		stime, _ := strconv.ParseFloat(fields[12], 64)
		rssPages, _ := strconv.ParseInt(fields[21], 10, 64)
		status, err := os.ReadFile(filepath.Join("/proc", pe.Name(), "status"))
		if err != nil {
			continue
		}
		uid := 0
		for _, l := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(l, "Uid:") {
				f := strings.Fields(l)
				if len(f) >= 2 {
					uid, _ = strconv.Atoi(f[1])
				}
				break
			}
		}
		if uidFilter > 0 && uid != uidFilter {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", pe.Name(), "cmdline"))
		command := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if command == "" {
			command = name
		}
		out = append(out, Process{
			PID: pid, UID: uid, Name: name,
			CPU:      (utime + stime) / 100.0,
			RSSBytes: rssPages * int64(os.Getpagesize()),
			Command:  command,
		})
	}
	return out, nil
}

// --- metric stream ---------------------------------------------------------------

// Metrics is a point-in-time snapshot pushed over the dashboard WebSocket.
type Metrics struct {
	Time      int64     `json:"t"`
	CPU       float64   `json:"cpu"`
	PerCore   []float64 `json:"per_core"`
	MemUsed   uint64    `json:"mem_used"`
	MemTotal  uint64    `json:"mem_total"`
	SwapUsed  uint64    `json:"swap_used"`
	SwapTotal uint64    `json:"swap_total"`
	NetRx     uint64    `json:"net_rx"`
	NetTx     uint64    `json:"net_tx"`
	Load      []float64 `json:"load"`
}

// MetricsSnapshot gathers current metrics for the realtime stream. Network
// counters are cumulative since boot; the frontend computes deltas.
func (s *System) MetricsSnapshot() Metrics {
	m := Metrics{Time: time.Now().UnixMilli()}
	if p, err := cpu.Percent(0, true); err == nil {
		var total float64
		for _, v := range p {
			total += v
		}
		if len(p) > 0 {
			m.CPU = total / float64(len(p))
		}
		m.PerCore = p
	}
	if mv, err := mem.VirtualMemory(); err == nil {
		m.MemUsed, m.MemTotal = mv.Used, mv.Total
	}
	if sw, err := mem.SwapMemory(); err == nil {
		m.SwapUsed, m.SwapTotal = sw.Used, sw.Total
	}
	if n, err := net.IOCounters(false); err == nil && len(n) > 0 {
		m.NetRx = n[0].BytesRecv
		m.NetTx = n[0].BytesSent
	}
	if l, err := load.Avg(); err == nil {
		m.Load = []float64{l.Load1, l.Load5, l.Load15}
	}
	return m
}

// ReadUptime returns the host uptime from /proc/uptime.
func ReadUptime() float64 {
	f, err := os.Open("/proc/uptime")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 0 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				return v
			}
		}
	}
	return 0
}
