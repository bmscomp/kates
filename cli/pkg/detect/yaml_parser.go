package detect

import (
	"strconv"

	"gopkg.in/yaml.v3"
)

type ResourceRequest struct {
	CPU    interface{} `yaml:"cpu"`
	Memory interface{} `yaml:"memory"`
}

type ComponentResources struct {
	Requests ResourceRequest `yaml:"requests"`
}

type KafkaComponent struct {
	Resources ComponentResources `yaml:"resources"`
}

type ZookeeperComponent struct {
	Resources ComponentResources `yaml:"resources"`
}

type CruiseControlComponent struct {
	Resources ComponentResources `yaml:"resources"`
}

type KafkaExporterComponent struct {
	Resources ComponentResources `yaml:"resources"`
}

type ValuesConfig struct {
	Kafka         KafkaComponent         `yaml:"kafka"`
	Zookeeper     ZookeeperComponent     `yaml:"zookeeper"`
	CruiseControl CruiseControlComponent `yaml:"cruiseControl"`
	KafkaExporter KafkaExporterComponent `yaml:"kafkaExporter"`
}

// quantityText is a YAML scalar as the quantity text it spells: a values file
// may write cpu: 2, cpu: 0.5 or cpu: "500m".
func quantityText(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	}
	return ""
}

// parseMemory reads a memory request in whole GiB, rounded up.
func parseMemory(v interface{}) int {
	return parseMemoryGiBCeil(quantityText(v))
}

// parseCPU reads a CPU request in millicores.
func parseCPU(v interface{}) int {
	return parseCPUMilli(quantityText(v))
}

func ParseValuesYAML(data []byte) ParsedReqs {
	var cfg ValuesConfig
	yaml.Unmarshal(data, &cfg)

	reqs := ParsedReqs{}

	if c := parseCPU(cfg.Kafka.Resources.Requests.CPU); c > 0 {
		reqs.BrokerCPU = c
	}
	if m := parseMemory(cfg.Kafka.Resources.Requests.Memory); m > 0 {
		reqs.BrokerMem = m
	}

	if c := parseCPU(cfg.Zookeeper.Resources.Requests.CPU); c > 0 {
		reqs.ControllerCPU = c
	}
	if m := parseMemory(cfg.Zookeeper.Resources.Requests.Memory); m > 0 {
		reqs.ControllerMem = m
	}

	oCpu, oMem := 0, 0
	oCpu += parseCPU(cfg.CruiseControl.Resources.Requests.CPU)
	oMem += parseMemory(cfg.CruiseControl.Resources.Requests.Memory)
	oCpu += parseCPU(cfg.KafkaExporter.Resources.Requests.CPU)
	oMem += parseMemory(cfg.KafkaExporter.Resources.Requests.Memory)

	reqs.OtherCPU = oCpu
	reqs.OtherMem = oMem

	return reqs
}
