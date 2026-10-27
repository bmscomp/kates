package detect

import "testing"

func TestParseMemoryMiB(t *testing.T) {
	for quantity, want := range map[string]float64{
		"256Mi":     256,
		"2Gi":       2048,
		"0.5Gi":     512,
		"524288Ki":  512,
		"268435456": 256,
		"1Ti":       1 << 20,
		"1G":        1e9 / (1 << 20),
		"500M":      500e6 / (1 << 20),
		"1k":        1e3 / (1 << 20),
		" 64Mi ":    64,
		"1e9":       1e9 / (1 << 20),
		"":          0,
		"lots":      0,
		"-1Gi":      0,
		"500m":      0, // millibytes: no request anyone writes
	} {
		if got := parseMemoryMiB(quantity); got != want {
			t.Errorf("parseMemoryMiB(%q) = %v, want %v", quantity, got, want)
		}
	}
}

func TestParseMemoryGiBCeil(t *testing.T) {
	for quantity, want := range map[string]int{
		"512Mi": 1,
		"2Gi":   2,
		"3G":    3, // 2.79 GiB, rounded up
		"":      0,
	} {
		if got := parseMemoryGiBCeil(quantity); got != want {
			t.Errorf("parseMemoryGiBCeil(%q) = %d, want %d", quantity, got, want)
		}
	}
}

func TestParseCPUMilli(t *testing.T) {
	for quantity, want := range map[string]int{
		"500m":  500,
		"2000m": 2000,
		"0.5":   500,
		"2":     2000,
		"1.5":   1500,
		" 4 ":   4000,
		"":      0,
		"two":   0,
		"-1":    0,
	} {
		if got := parseCPUMilli(quantity); got != want {
			t.Errorf("parseCPUMilli(%q) = %d, want %d", quantity, got, want)
		}
	}
}

func TestParseJVMHeapMiB(t *testing.T) {
	for heap, want := range map[string]int{
		"768m":    768,
		"768M":    768,
		"2g":      2048,
		"1048576": 1,
		"512k":    0,
		"":        0,
	} {
		if got := parseJVMHeapMiB(heap); got != want {
			t.Errorf("parseJVMHeapMiB(%q) = %d, want %d", heap, got, want)
		}
	}
}

// ParseValuesYAML reads YAML numbers as well as quoted quantities: cpu: 0.5
// used to read as 0, and memory: 512Mi as nothing at all.
func TestParseValuesYAMLQuantities(t *testing.T) {
	reqs := ParseValuesYAML([]byte(`
kafka:
  resources:
    requests:
      cpu: 0.5
      memory: 512Mi
zookeeper:
  resources:
    requests:
      cpu: 2
      memory: 2Gi
cruiseControl:
  resources:
    requests:
      cpu: "250m"
      memory: 1G
`))
	if reqs.BrokerCPU != 500 || reqs.BrokerMem != 1 {
		t.Errorf("broker = %dm/%dGi, want 500m/1Gi", reqs.BrokerCPU, reqs.BrokerMem)
	}
	if reqs.ControllerCPU != 2000 || reqs.ControllerMem != 2 {
		t.Errorf("controller = %dm/%dGi, want 2000m/2Gi", reqs.ControllerCPU, reqs.ControllerMem)
	}
	if reqs.OtherCPU != 250 || reqs.OtherMem != 1 {
		t.Errorf("other = %dm/%dGi, want 250m/1Gi", reqs.OtherCPU, reqs.OtherMem)
	}
}
