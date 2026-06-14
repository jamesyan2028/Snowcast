package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var logTimeRE = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+(.*)$`)
var benchPhaseRE = regexp.MustCompile(`BENCH phase=(\S+)\s+ms=(\d+)`)

func parseLogTime(line string) (time.Time, string, bool) {
	m := logTimeRE.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, "", false
	}
	layout := "2006/01/02 15:04:05"
	tsStr := m[1]
	if strings.Contains(tsStr, ".") {
		layout = "2006/01/02 15:04:05.000000"
	}
	ts, err := time.ParseInLocation(layout, tsStr, time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	return ts, m[2], true
}

func integratedRecoverMS(logPath string) (float64, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var firstTS time.Time
	var readyTS time.Time
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ts, msg, ok := parseLogTime(sc.Text())
		if !ok {
			continue
		}
		if strings.Contains(msg, "Snowcast server started") && firstTS.IsZero() {
			firstTS = ts
		}
		if strings.Contains(msg, "Backup ready at") {
			readyTS = ts
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if firstTS.IsZero() || readyTS.IsZero() {
		return 0, fmt.Errorf("could not find Backup ready in %s", logPath)
	}
	return float64(readyTS.Sub(firstTS).Microseconds()) / 1000.0, nil
}

func parseBenchPhases(logPath string) (map[string]float64, error) {
	phases := make(map[string]float64)
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "BENCH phase="); idx >= 0 {
			m := benchPhaseRE.FindStringSubmatch(line[idx:])
			if m == nil {
				continue
			}
			ms, _ := strconv.ParseFloat(m[2], 64)
			phases[m[1]] = ms
		}
	}
	return phases, sc.Err()
}

func findLogEventTime(logPath, substr string) (time.Time, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ts, msg, ok := parseLogTime(sc.Text())
		if ok && strings.Contains(msg, substr) {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("event %q not found in %s", substr, logPath)
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func summarizeLatencies(samples []float64) map[string]float64 {
	out := map[string]float64{"p50": 0, "p95": 0, "p99": 0, "max": 0}
	if len(samples) == 0 {
		return out
	}
	cp := append([]float64(nil), samples...)
	sort.Float64s(cp)
	out["p50"] = percentile(cp, 0.50)
	out["p95"] = percentile(cp, 0.95)
	out["p99"] = percentile(cp, 0.99)
	out["max"] = cp[len(cp)-1]
	return out
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

func parseIntList(s string, def []int) []int {
	if s == "" {
		return def
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func tempWalPath(name string) string {
	return filepath.Join(os.TempDir(), name)
}
