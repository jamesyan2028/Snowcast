package bench

import (
	"log"
	"os"
	"time"
)

func enabled() bool {
	return os.Getenv("SNOWCAST_BENCH") == "1"
}

// Mark logs elapsed time since start when SNOWCAST_BENCH=1.
func Mark(phase string, start time.Time) {
	if !enabled() {
		return
	}
	ms := time.Since(start).Milliseconds()
	log.Printf("BENCH phase=%s ms=%d", phase, ms)
}

// MarkInstant logs a phase with zero duration (event timestamp).
func MarkInstant(phase string) {
	if !enabled() {
		return
	}
	log.Printf("BENCH phase=%s ms=0", phase)
}
