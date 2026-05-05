package detect

import (
	"strconv"
	"strings"

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

func parseMemory(v interface{}) int {
	if v == nil {
		return 0
	}
	s := ""
	switch val := v.(type) {
	case string:
		s = val
	case int:
		s = strconv.Itoa(val)
	}

	s = strings.TrimSuffix(s, "i")
	if strings.HasSuffix(s, "G") {
		val, _ := strconv.Atoi(strings.TrimSuffix(s, "G"))
		return val
	}
	if strings.HasSuffix(s, "M") {
		val, _ := strconv.Atoi(strings.TrimSuffix(s, "M"))
		return val / 1024
	}
	return 0
}

func parseCPU(v interface{}) int {
	if v == nil {
		return 0
	}
	s := ""
	switch val := v.(type) {
	case string:
		s = val
	case int:
		s = strconv.Itoa(val)
	}

	if strings.HasSuffix(s, "m") {
		val, _ := strconv.Atoi(strings.TrimSuffix(s, "m"))
		return val
	}
	val, _ := strconv.Atoi(s)
	return val * 1000
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
