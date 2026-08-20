package sysmetrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Snapshot struct {
	CPUPercent     float64  `json:"cpu_percent"`
	MemUsedBytes   uint64   `json:"mem_used_bytes"`
	MemTotalBytes  uint64   `json:"mem_total_bytes"`
	MemPercent     float64  `json:"mem_percent"`
	DiskUsedBytes  uint64   `json:"disk_used_bytes"`
	DiskTotalBytes uint64   `json:"disk_total_bytes"`
	DiskPercent    float64  `json:"disk_percent"`
	DiskPath       string   `json:"disk_path,omitempty"`
	GPUPercent     *float64 `json:"gpu_percent,omitempty"`
	GPUMemUsedMB   *float64 `json:"gpu_mem_used_mb,omitempty"`
	GPUMemTotalMB  *float64 `json:"gpu_mem_total_mb,omitempty"`
	GPUAvailable   bool     `json:"gpu_available"`
}

type Collector struct {
	mu        sync.Mutex
	prevIdle  uint64
	prevTotal uint64
	havePrev  bool
	diskPath  string
}

func NewCollector(diskPath string) *Collector {
	if diskPath == "" {
		diskPath = "/"
	}
	c := &Collector{diskPath: diskPath}
	// Prime CPU counters so the first API call has a meaningful delta.
	_, _ = c.cpuPercent()
	return c
}

func (c *Collector) Snapshot() Snapshot {
	s := Snapshot{DiskPath: c.diskPath}

	if pct, err := c.cpuPercent(); err == nil {
		s.CPUPercent = pct
	}
	if used, total, err := memUsage(); err == nil && total > 0 {
		s.MemUsedBytes = used
		s.MemTotalBytes = total
		s.MemPercent = float64(used) / float64(total) * 100
	}
	if used, total, err := diskUsage(c.diskPath); err == nil && total > 0 {
		s.DiskUsedBytes = used
		s.DiskTotalBytes = total
		s.DiskPercent = float64(used) / float64(total) * 100
	}
	if gpuUtil, memUsed, memTotal, ok := gpuStats(); ok {
		s.GPUAvailable = true
		s.GPUPercent = &gpuUtil
		s.GPUMemUsedMB = &memUsed
		s.GPUMemTotalMB = &memTotal
	}
	return s
}

func (c *Collector) cpuPercent() (float64, error) {
	idle, total, err := readCPUTimes()
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.havePrev {
		c.prevIdle = idle
		c.prevTotal = total
		c.havePrev = true
		time.Sleep(120 * time.Millisecond)
		idle2, total2, err := readCPUTimes()
		if err != nil {
			return 0, err
		}
		c.prevIdle = idle2
		c.prevTotal = total2
		// Still return based on short sample
		dIdle := idle2 - idle
		dTotal := total2 - total
		if dTotal == 0 {
			return 0, nil
		}
		return (1 - float64(dIdle)/float64(dTotal)) * 100, nil
	}

	dIdle := idle - c.prevIdle
	dTotal := total - c.prevTotal
	c.prevIdle = idle
	c.prevTotal = total
	if dTotal == 0 {
		return 0, nil
	}
	pct := (1 - float64(dIdle)/float64(dTotal)) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}

func readCPUTimes() (idle, total uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, fmt.Errorf("empty /proc/stat")
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat line")
	}
	var vals []uint64
	for _, f := range fields[1:] {
		v, e := strconv.ParseUint(f, 10, 64)
		if e != nil {
			return 0, 0, e
		}
		vals = append(vals, v)
	}
	// user nice system idle iowait irq softirq steal ...
	idle = vals[3]
	if len(vals) > 4 {
		idle += vals[4] // iowait
	}
	for _, v := range vals {
		total += v
	}
	return idle, total, nil
}

func memUsage() (used, total uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var memTotal, memAvailable uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			memTotal = parseMemKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			memAvailable = parseMemKB(line)
		}
	}
	if memTotal == 0 {
		return 0, 0, fmt.Errorf("MemTotal missing")
	}
	used = (memTotal - memAvailable) * 1024
	total = memTotal * 1024
	return used, total, nil
}

func parseMemKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

func gpuStats() (util, memUsedMB, memTotalMB float64, ok bool) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits",
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, false
	}
	line := strings.TrimSpace(string(bytes.Split(out, []byte("\n"))[0]))
	parts := strings.Split(line, ",")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	util, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	memUsedMB, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	memTotalMB, err3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return util, memUsedMB, memTotalMB, true
}
