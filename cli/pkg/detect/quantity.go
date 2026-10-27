package detect

import (
	"math"
	"strconv"
	"strings"
)

// Kubernetes resource quantities are read here and nowhere else. The module
// does not depend on k8s.io/apimachinery, so these cover the forms a
// cluster and a values file actually carry rather than the full grammar.

// memUnits are the Kubernetes memory quantity suffixes, in bytes. A binary
// suffix ends in "i", so "Mi" and "M" cannot both match; binary ones are
// tried first.
var memUnits = []struct {
	suffix string
	bytes  float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15}, {"E", 1e18},
}

// parseMemoryMiB reads a memory quantity as MiB: a number of bytes, plain or
// in exponent form, with or without one of memUnits ("512Mi", "1G",
// "0.5Gi", "268435456"). Anything else, an empty quantity included, reads as
// 0.
func parseMemoryMiB(quantity string) float64 {
	quantity = strings.TrimSpace(quantity)
	bytes := 1.0
	for _, u := range memUnits {
		if n, ok := strings.CutSuffix(quantity, u.suffix); ok {
			quantity, bytes = n, u.bytes
			break
		}
	}
	n, err := strconv.ParseFloat(quantity, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n * bytes / (1 << 20)
}

// parseMemoryGiBCeil is parseMemoryMiB in whole GiB, rounded up: a budget
// sums requests, and a 512Mi request is not free.
func parseMemoryGiBCeil(quantity string) int {
	return int(math.Ceil(parseMemoryMiB(quantity) / 1024))
}

// parseCPUMilli reads a CPU quantity as millicores: "500m", "2", "0.5",
// "1.5". Anything else, an empty quantity included, reads as 0.
func parseCPUMilli(quantity string) int {
	quantity = strings.TrimSpace(quantity)
	scale := 1000.0
	if n, ok := strings.CutSuffix(quantity, "m"); ok {
		quantity, scale = n, 1
	}
	n, err := strconv.ParseFloat(quantity, 64)
	if err != nil || n < 0 {
		return 0
	}
	return int(math.Round(n * scale))
}

// parseJVMHeapMiB reads a JVM heap size (-Xmx768m, -Xms2g) as MiB. The JVM
// suffixes are single letters, either case, and binary: "m" is MiB here,
// not the millis it is in a Kubernetes quantity. A bare number is bytes.
func parseJVMHeapMiB(heap string) int {
	heap = strings.TrimSpace(heap)
	if heap == "" {
		return 0
	}
	switch heap[len(heap)-1] {
	case 'k', 'K':
		heap = heap[:len(heap)-1] + "Ki"
	case 'm', 'M':
		heap = heap[:len(heap)-1] + "Mi"
	case 'g', 'G':
		heap = heap[:len(heap)-1] + "Gi"
	case 't', 'T':
		heap = heap[:len(heap)-1] + "Ti"
	}
	return int(parseMemoryMiB(heap))
}
