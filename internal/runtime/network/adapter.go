package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	harnessruntime "github.com/urunc-dev/evaluation_suite/internal/runtime"
)

const defaultImage = "networkstatic/iperf3:latest"

type commandRunner func(context.Context, ...string) ([]byte, []byte, error)

// Adapter runs both ends of an iperf3 benchmark through the nerdctl CLI.
// It deliberately has no containerd client dependency.
type Adapter struct {
	run           commandRunner
	serverName    string
	serverIP      string
	serverRunning bool
	networkName   string
}

func NewAdapter() *Adapter { return &Adapter{run: runNerdctl} }

func (a *Adapter) ExperimentName() string { return "network" }

func (a *Adapter) Prepare(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	log.Printf("Preparing network benchmark trial=%s runtime=%s handler=%s image=%s\n", tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc))
	startedAt := time.Now()
	stdout, stderr, err := a.run(ctx, "pull", image(tc))
	result := stageResult(harnessruntime.StagePrepare, startedAt, tc, "Pull iperf3 image", map[string]any{
		"stdout": strings.TrimSpace(string(stdout)),
		"stderr": strings.TrimSpace(string(stderr)),
	})
	if err != nil {
		return result, commandError("pull iperf3 image", err, stdout, stderr)
	}
	return result, nil
}

func (a *Adapter) CreateTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	log.Printf("Creating network benchmark trial=%s runtime=%s handler=%s image=%s\n", tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc))
	startedAt := time.Now()

	a.networkName = tc.Trial.ID + "-iperf3-network"
	// 1. Create a dedicated network for the iperf3 server and client to communicate
	stdout, stderr, err := a.run(ctx, "network", "create", a.networkName)
	if err != nil {
		result := stageResult(harnessruntime.StageCreate, startedAt, tc, "Create iperf3 network", map[string]any{
			"stdout": strings.TrimSpace(string(stdout)),
			"stderr": strings.TrimSpace(string(stderr)),
		})
		return result, commandError("create iperf3 network", err, stdout, stderr)
	}

	// add some delay to ensure the network is ready before starting the server
	time.Sleep(2 * time.Second)

	a.serverName = tc.Trial.ID + "-iperf3-server"

	args := []string{
		"run",
		"-it",
		"--name", a.serverName,
		"--runtime", tc.Trial.RuntimeHandler,
		"--network", a.networkName,
		image(tc), "-s", "--one-off",
	}

	log.Printf("Starting background server: nerdctl %s\n", strings.Join(args, " "))

	// 2. Use context.Background() so the server isn't killed if the parent context cancels
	cmd := exec.CommandContext(context.Background(), "nerdctl", args...)

	// temporary log file to capture the server's output
	logFile, err := os.CreateTemp("", "iperf3-server-*.log")
	if err != nil {
		panic(err)
	}
	defer logFile.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		panic(err)
	}
	defer devNull.Close()

	cmd.Stdin = os.Stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Detach from the parent's terminal/session.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		result := stageResult(harnessruntime.StageCreate, startedAt, tc, "Start iperf3 server", nil)
		return result, fmt.Errorf("failed to start iperf3 server: %w", err)
	}

	a.serverRunning = true

	return stageResult(harnessruntime.StageCreate, startedAt, tc, "Start iperf3 server", map[string]any{
		"server_name": a.serverName,
		"server_ip":   a.serverIP,
	}), nil
}

func (a *Adapter) StartTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	log.Printf("Starting network benchmark trial=%s runtime=%s handler=%s image=%s\n", tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc))
	startedAt := time.Now()
	// wait for a few seconds to ensure the server is ready
	time.Sleep(2 * time.Second)

	clientName := tc.Trial.ID + "-iperf3-client"
	stdout, stderr, err := a.run(ctx,
		"run", "-it", "--rm", "--name", clientName,
		"--runtime", tc.Trial.RuntimeHandler,
		"--network", a.networkName,
		image(tc), "-c", a.serverName, "--json",
	)
	if err != nil {
		_ = a.removeServer(context.Background())
		result := stageResult(harnessruntime.StageStart, startedAt, tc, "Run iperf3 client", map[string]any{
			"stderr": strings.TrimSpace(string(stderr)),
		})
		return result, commandError("run iperf3 client", err, stdout, stderr)
	}

	jsonOutput, err := ExtractJSONObject(stdout)
	if err != nil {
		_ = a.removeServer(context.Background())

		result := stageResult(
			harnessruntime.StageStart,
			startedAt,
			tc,
			"Extract iperf3 JSON",
			map[string]any{
				"stdout": strings.TrimSpace(string(stdout)),
				"stderr": strings.TrimSpace(string(stderr)),
			},
		)

		return result, fmt.Errorf("could not extract iperf3 JSON: %w", err)
	}

	var iperfResult map[string]any
	if err := json.Unmarshal(jsonOutput, &iperfResult); err != nil {
		_ = a.removeServer(context.Background())
		result := stageResult(harnessruntime.StageStart, startedAt, tc, "Run iperf3 client", map[string]any{
			"stderr": strings.TrimSpace(string(stderr)),
		})
		return result, fmt.Errorf("iperf3 returned invalid JSON: %w; stdout=%q", err, stdout)
	}

	return stageResult(harnessruntime.StageStart, startedAt, tc, "Run iperf3 client", map[string]any{
		"server_ip": a.serverIP,
		"iperf3":    iperfResult,
		"stderr":    strings.TrimSpace(string(stderr)),
	}), nil
}

func (a *Adapter) WaitReady(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return completedStage(ctx, harnessruntime.StageWaitReady, "iperf3 client completed", tc)
}

func (a *Adapter) Stop(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	log.Printf("Stopping network benchmark trial=%s runtime=%s handler=%s image=%s\n", tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc))
	startedAt := time.Now()
	err := a.removeServer(ctx)
	result := stageResult(harnessruntime.StageStop, startedAt, tc, "Remove iperf3 server", nil)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (a *Adapter) DeleteTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	return completedStage(ctx, harnessruntime.StageDelete, "iperf3 containers removed", tc)
}

func (a *Adapter) Cleanup(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	log.Printf("Cleaning up network benchmark trial=%s runtime=%s handler=%s image=%s\n", tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc))
	startedAt := time.Now()
	err := a.removeServer(ctx)
	result := stageResult(harnessruntime.StageCleanup, startedAt, tc, "Clean network benchmark resources", nil)
	if err != nil {
		return result, err
	}
	// Remove the dedicated network
	stdout, stderr, err := a.run(ctx, "network", "rm", a.networkName)
	if err != nil {
		result := stageResult(harnessruntime.StageCleanup, startedAt, tc, "Remove iperf3 network", map[string]any{
			"stdout": strings.TrimSpace(string(stdout)),
			"stderr": strings.TrimSpace(string(stderr)),
		})
		return result, commandError("remove iperf3 network", err, stdout, stderr)
	}

	return result, nil
}

func (a *Adapter) removeServer(ctx context.Context) error {
	if !a.serverRunning {
		return nil
	}
	stdout, stderr, err := a.run(ctx, "rm", "-f", a.serverName)
	if err != nil {
		return commandError("remove iperf3 server", err, stdout, stderr)
	}
	a.serverRunning = false
	return nil
}

func image(tc harnessruntime.TrialContext) string {
	if tc.Trial.Image == "" {
		return defaultImage
	}
	return tc.Trial.Image
}

func runNerdctl(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "nerdctl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// print the command being run for debugging purposes
	log.Printf("Running command: nerdctl %s\n", strings.Join(args, " "))
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func commandError(action string, err error, stdout, stderr []byte) error {
	return fmt.Errorf("%s: %w; stdout=%q; stderr=%q", action, err, stdout, stderr)
}

func completedStage(ctx context.Context, stage harnessruntime.Stage, description string, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return harnessruntime.StageResult{}, err
	}
	return stageResult(stage, startedAt, tc, description, nil), nil
}

func stageResult(stage harnessruntime.Stage, startedAt time.Time, tc harnessruntime.TrialContext, description string, data map[string]any) harnessruntime.StageResult {
	finishedAt := time.Now()
	return harnessruntime.StageResult{
		Stage:       stage,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Duration:    finishedAt.Sub(startedAt),
		Description: fmt.Sprintf("%s: trial=%s runtime=%s handler=%s image=%s", description, tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, image(tc)),
		Data:        data,
	}
}

// ExtractJSONObject finds and returns the first valid JSON object embedded
// anywhere in noisy command output.
//
// It handles output containing:
//   - ANSI terminal escape sequences
//   - warnings before the JSON
//   - SeaBIOS/iPXE boot output
//   - logs or kernel messages after the JSON
func ExtractJSONObject(output []byte) ([]byte, error) {
	for offset := 0; offset < len(output); {
		relativeStart := bytes.IndexByte(output[offset:], '{')
		if relativeStart == -1 {
			break
		}

		start := offset + relativeStart
		candidate := output[start:]

		decoder := json.NewDecoder(bytes.NewReader(candidate))

		var value json.RawMessage
		if err := decoder.Decode(&value); err == nil {
			// Ensure the extracted JSON is an object rather than an array,
			// string, number, boolean, or null.
			trimmed := bytes.TrimSpace(value)
			if len(trimmed) > 0 && trimmed[0] == '{' {
				result := make([]byte, len(trimmed))
				copy(result, trimmed)
				return result, nil
			}
		}

		// This opening brace was not the beginning of valid JSON.
		// Continue searching from the following byte.
		offset = start + 1
	}

	return nil, fmt.Errorf("no valid JSON object found in output")
}
