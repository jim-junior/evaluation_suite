package cpu

import (
	"reflect"
	"testing"

	"github.com/urunc-dev/evaluation_suite/internal/plan"
	harnessruntime "github.com/urunc-dev/evaluation_suite/internal/runtime"
)

func TestNerdctlArgs(t *testing.T) {
	tc := harnessruntime.TrialContext{Trial: plan.Trial{
		RuntimeHandler: "io.containerd.runc.v2",
		Image:          "jimjuniorb/stress-ng-benchmark:0.1",
		CPU:            1,
		CPUMethod:      "matrixprod",
		Timeout:        "30s",
		MetricsBrief:   true,
	}}

	want := []string{
		"run", "-it", "--rm", "--runtime=io.containerd.runc.v2",
		"jimjuniorb/stress-ng-benchmark:0.1",
		"--cpu", "1", "--cpu-method", "matrixprod", "--timeout", "30s", "--metrics-brief",
	}
	if got := nerdctlArgs(tc); !reflect.DeepEqual(got, want) {
		t.Fatalf("nerdctlArgs() = %#v, want %#v", got, want)
	}
}
