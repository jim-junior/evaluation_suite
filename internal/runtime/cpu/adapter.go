package cpu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	harnessruntime "github.com/urunc-dev/evaluation_suite/internal/runtime"
)

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) ExperimentName() string {
	return "cpu"
}

func (a *Adapter) Prepare(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StagePrepare, tc)
}

func (a *Adapter) CreateTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StageCreate, tc)
}

func (a *Adapter) StartTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	args := nerdctlArgs(tc)
	cmd := exec.CommandContext(ctx, "nerdctl", args...)

	log.Println("Running Command: ", cmd.String())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout // The benchmark image writes YAML only to stdout.
	cmd.Stderr = &stderr // stress-ng and nerdctl logs remain separate.

	err := cmd.Run()
	finishedAt := time.Now()
	result := harnessruntime.StageResult{
		Stage:      harnessruntime.StageStart,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Duration:   finishedAt.Sub(startedAt),
		Description: fmt.Sprintf(
			"Run CPU benchmark: trial=%s runtime=%s handler=%s image=%s",
			tc.Trial.ID,
			tc.Trial.RuntimeName,
			tc.Trial.RuntimeHandler,
			tc.Trial.Image,
		),
		Data: map[string]any{
			"stderr": stderr.String(),
		},
	}

	if err != nil {
		return result, fmt.Errorf("run CPU benchmark with nerdctl: %w; stderr: %s", err, stderr.String())
	}

	fmt.Println(stdout.String())

	benchmarkJSON, err := YAMLToJSON(stdout.String())
	if err != nil {
		return result, fmt.Errorf("convert stress-ng YAML output to JSON: %w; stdout: %s; stderr: %s", err, stdout.String(), stderr.String())
	}

	result.Data = map[string]any{
		"benchmark": benchmarkJSON,
		"stderr":    stderr.String(),
	}
	return result, nil
}

func (a *Adapter) WaitReady(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StageWaitReady, tc)
}

func (a *Adapter) Stop(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StageStop, tc)
}

func (a *Adapter) DeleteTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StageDelete, tc)
}

func (a *Adapter) Cleanup(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return noOpStage(ctx, harnessruntime.StageCleanup, tc)
}

func nerdctlArgs(tc harnessruntime.TrialContext) []string {
	args := []string{
		"run",
		"-it",
		"--rm",
		"--runtime=" + tc.Trial.RuntimeHandler,
		tc.Trial.Image,
		"--cpu", strconv.Itoa(tc.Trial.CPU),
		"--cpu-method", tc.Trial.CPUMethod,
		"--timeout", tc.Trial.Timeout,
	}
	if tc.Trial.MetricsBrief {
		args = append(args, "--metrics-brief")
	}
	return args
}

// YAMLToJSON extracts a YAML document embedded in stdout, converts it to JSON,
// and returns the resulting raw JSON.
//
// It expects the YAML document to begin with a line containing "---".
// The terminating "..." marker is optional.
func YAMLToJSON(stdout string) (json.RawMessage, error) {
	yamlDocument, err := extractYAMLDocument(stdout)
	if err != nil {
		return nil, err
	}

	var value any
	if err := yaml.Unmarshal([]byte(yamlDocument), &value); err != nil {
		return nil, fmt.Errorf("decode stress-ng YAML: %w", err)
	}

	jsonData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode stress-ng result as JSON: %w", err)
	}

	return json.RawMessage(jsonData), nil
}

// extractYAMLDocument extracts the content beginning at the YAML document
// marker "---" and ending at the optional document marker "...".
func extractYAMLDocument(output string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")

	start := -1
	end := len(lines)

	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "---":
			if start == -1 {
				start = index
			}
		case "...":
			if start != -1 {
				end = index + 1
				goto extracted
			}
		}
	}

extracted:
	if start == -1 {
		return "", errors.New("YAML document marker '---' not found in stress-ng output")
	}

	document := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if document == "" {
		return "", errors.New("stress-ng YAML document is empty")
	}

	return document, nil
}
func noOpStage(ctx context.Context, stage harnessruntime.Stage, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return harnessruntime.StageResult{}, err
	}
	finishedAt := time.Now()
	return harnessruntime.StageResult{
		Stage:       stage,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Duration:    finishedAt.Sub(startedAt),
		Description: fmt.Sprintf("No-op for CLI CPU benchmark: trial=%s", tc.Trial.ID),
	}, nil
}
