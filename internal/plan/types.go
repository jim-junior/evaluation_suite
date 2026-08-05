package plan

import "github.com/urunc-dev/evaluation_suite/internal/manifest"

type Plan struct {
	Version string  `json:"version"`
	Trials  []Trial `json:"trials"`
}

type Trial struct {
	ID             string            `json:"id"`
	ExperimentName string            `json:"experimentName"`
	WorkloadName   string            `json:"workloadName"`
	RuntimeName    string            `json:"runtimeName"`
	RuntimeHandler string            `json:"runtimeHandler"`
	Image          string            `json:"image"`
	Ports          *manifest.Ports   `json:"ports,omitempty"`
	Volumes        []manifest.Volume `json:"volumes,omitempty"`
	Snapshotter    string            `json:"snapshotter,omitempty"`
	CPU            int               `json:"cpu,omitempty"`
	CPUMethod      string            `json:"cpuMethod,omitempty"`
	Timeout        string            `json:"timeout,omitempty"`
	MetricsBrief   bool              `json:"metricsBrief,omitempty"`
}
