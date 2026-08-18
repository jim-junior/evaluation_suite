package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	harnessruntime "github.com/urunc-dev/evaluation_suite/internal/runtime"
	"github.com/urunc-dev/evaluation_suite/internal/utils"
)

type commandRunner func(context.Context, ...string) ([]byte, error)

// Adapter measures the container cgroup and its containerd shim using only
// nerdctl and Linux proc/cgroup files.
type Adapter struct {
	run        commandRunner
	procRoot   string
	cgroupRoot string
}

type CgroupMetrics struct {
	CurrentBytes uint64            `json:"currentBytes"`
	PeakBytes    uint64            `json:"peakBytes"`
	Stat         map[string]uint64 `json:"stat"`
}

type ShimMetrics struct {
	PID      int    `json:"pid"`
	PSSBytes uint64 `json:"pssBytes"`
	USSBytes uint64 `json:"ussBytes"`
	RSSBytes uint64 `json:"rssBytes"`
}

type Metrics struct {
	ContainerID  string        `json:"containerId"`
	ContainerPID int           `json:"containerPid"`
	CgroupPath   string        `json:"cgroupPath"`
	Cgroup       CgroupMetrics `json:"cgroup"`
	Shim         ShimMetrics   `json:"shim"`
}

func NewAdapter() *Adapter {
	return &Adapter{
		run:        runNerdctl,
		procRoot:   "/proc",
		cgroupRoot: "/sys/fs/cgroup",
	}
}

func (a *Adapter) ExperimentName() string { return "memory" }

func (a *Adapter) Prepare(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	err := pullImage(ctx, tc.Trial.Image)
	result := stageResult(harnessruntime.StagePrepare, "Pull memory benchmark image", tc, startedAt, nil)
	if err != nil {
		return result, fmt.Errorf("pull image %s: %w", tc.Trial.Image, err)
	}
	return result, nil
}

func (a *Adapter) CreateTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	return stageResult(
		harnessruntime.StageCreate,
		"Create memory benchmark container", tc, startedAt, nil,
	), nil
}

func (a *Adapter) StartTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()

	args := []string{"run", "-it", "--name", tc.Trial.ID, "--runtime", tc.Trial.RuntimeHandler, tc.Trial.Image}

	cmd := exec.CommandContext(ctx, "nerdctl", args...)
	cmd.Stdin = os.Stdin

	// Detach from the parent's terminal/session.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		_ = a.cleanupbenchmarkresources(ctx, tc)
		return stageResult(harnessruntime.StageStart, "Start memory benchmark container", tc, startedAt, nil), fmt.Errorf("start container: %w", err)
	}

	go func() { _ = cmd.Wait() }()

	return stageResult(harnessruntime.StageStart, "Start memory benchmark container", tc, startedAt, nil), nil

}

func (a *Adapter) WaitReady(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()

	time.Sleep(2 * time.Second)

	metrics, err := a.collect(ctx, tc.Trial.ID)
	result := stageResult(harnessruntime.StageWaitReady, "Collect memory metrics", tc, startedAt, metrics)
	if err != nil {
		_ = a.cleanupbenchmarkresources(ctx, tc)
		return result, fmt.Errorf("collect memory metrics: %w", err)
	}

	return result, nil
}

func (a *Adapter) Stop(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return a.commandStage(ctx, harnessruntime.StageStop, "Stop memory benchmark container", tc, "stop", tc.Trial.ID)
}

func (a *Adapter) DeleteTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return a.commandStage(ctx, harnessruntime.StageDelete, "Remove memory benchmark container", tc, "rm", "--force", tc.Trial.ID)
}

func (a *Adapter) Cleanup(_ context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return stageResult(harnessruntime.StageCleanup, "Cleanup memory benchmark container", tc, time.Now(), nil), nil
}

func (a *Adapter) cleanupbenchmarkresources(ctx context.Context, tc harnessruntime.TrialContext) error {
	_, err := a.run(ctx, "rm", "--force", tc.Trial.ID)
	return err
}

func (a *Adapter) commandStage(
	ctx context.Context,
	stage harnessruntime.Stage,
	description string,
	tc harnessruntime.TrialContext,
	args ...string,
) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	output, err := a.run(ctx, args...)
	result := stageResult(stage, description, tc, startedAt, map[string]any{
		"output": strings.TrimSpace(string(output)),
	})
	if err != nil {
		return result, fmt.Errorf("nerdctl %s: %w", strings.Join(args, " "), err)
	}
	return result, nil
}

func (a *Adapter) collect(ctx context.Context, name string) (Metrics, error) {
	pidText, err := a.run(ctx, "inspect", "--format", "{{.State.Pid}}", name)
	if err != nil {
		return Metrics{}, fmt.Errorf("inspect container PID: %w", err)
	}
	containerPID, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil || containerPID <= 0 {
		return Metrics{}, fmt.Errorf("invalid container PID %q", strings.TrimSpace(string(pidText)))
	}

	idText, err := a.run(ctx, "inspect", "--format", "{{.Id}}", name)
	if err != nil {
		return Metrics{}, fmt.Errorf("inspect container ID: %w", err)
	}
	containerID := strings.TrimSpace(string(idText))
	if containerID == "" {
		return Metrics{}, errors.New("nerdctl returned an empty container ID")
	}

	cgroupPath, err := readCgroupPath(filepath.Join(a.procRoot, strconv.Itoa(containerPID), "cgroup"))
	if err != nil {
		return Metrics{}, err
	}
	cgroupDir := filepath.Join(a.cgroupRoot, strings.TrimPrefix(cgroupPath, "/"))
	cgroup, err := readCgroupMetrics(cgroupDir)
	if err != nil {
		return Metrics{}, err
	}

	shimPID, err := findShimPID(a.procRoot, containerID)
	if err != nil {
		return Metrics{}, err
	}
	shim, err := readShimMetrics(filepath.Join(a.procRoot, strconv.Itoa(shimPID), "smaps_rollup"), shimPID)
	if err != nil {
		fmt.Println(err)
		return Metrics{}, err
	}

	return Metrics{
		ContainerID:  containerID,
		ContainerPID: containerPID,
		CgroupPath:   cgroupDir,
		Cgroup:       cgroup,
		Shim:         shim,
	}, nil
}

func runNerdctl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nerdctl", args...)
	cmd.Stdin = os.Stdin
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func stageResult(
	stage harnessruntime.Stage,
	description string,
	tc harnessruntime.TrialContext,
	startedAt time.Time,
	data any,
) harnessruntime.StageResult {
	finishedAt := time.Now()
	return harnessruntime.StageResult{
		Stage:       stage,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Duration:    finishedAt.Sub(startedAt),
		Description: fmt.Sprintf("%s: trial=%s runtime=%s handler=%s image=%s", description, tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, tc.Trial.Image),
		Data:        data,
	}
}

func (a *Adapter) GenerateResult(ctx context.Context, tc harnessruntime.TrialContext, results []harnessruntime.StageResult) (any, error) {
	// For this adapter, we can return the metrics collected during the WaitReady stages. there can be multiple WaitReady stages, so we need the mean of cgroups and shims metrics.

	var cgroupMetricsList []CgroupMetrics
	var shimMetricsList []ShimMetrics

	for _, result := range results {
		if result.Stage == harnessruntime.StageWaitReady {
			metrics, ok := result.Data.(Metrics)
			if !ok {
				return nil, fmt.Errorf("invalid data type for stage %s: expected Metrics, got %T", result.Stage, result.Data)
			}
			cgroupMetricsList = append(cgroupMetricsList, metrics.Cgroup)
			shimMetricsList = append(shimMetricsList, metrics.Shim)
		}
	}

	if len(cgroupMetricsList) == 0 || len(shimMetricsList) == 0 {
		return nil, errors.New("no metrics collected during WaitReady stages")
	}

	// Calculate mean of cgroup metrics
	meanCgroupMetrics := CgroupMetrics{
		CurrentBytes: uint64(utils.Mean(
			func() []float64 {
				values := make([]float64, len(cgroupMetricsList))
				for i, m := range cgroupMetricsList {
					values[i] = float64(m.CurrentBytes)
				}
				return values
			}())),
		PeakBytes: uint64(utils.Mean(
			func() []float64 {
				values := make([]float64, len(cgroupMetricsList))
				for i, m := range cgroupMetricsList {
					values[i] = float64(m.PeakBytes)
				}
				return values
			}())),
	}

	// Calculate mean of shim metrics
	meanShimMetrics := ShimMetrics{
		USSBytes: uint64(utils.Mean(
			func() []float64 {
				values := make([]float64, len(shimMetricsList))
				for i, m := range shimMetricsList {
					values[i] = float64(m.USSBytes)
				}
				return values
			}())),
		RSSBytes: uint64(utils.Mean(
			func() []float64 {
				values := make([]float64, len(shimMetricsList))
				for i, m := range shimMetricsList {
					values[i] = float64(m.RSSBytes)
				}
				return values
			}())),
	}

	return Metrics{
		Cgroup: meanCgroupMetrics,
		Shim:   meanShimMetrics,
	}, nil
}
