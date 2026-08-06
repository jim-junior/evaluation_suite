package httpreadiness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	harnessruntime "github.com/urunc-dev/evaluation_suite/internal/runtime"
)

const (
	retryInterval  = time.Millisecond
	requestTimeout = 100 * time.Millisecond
	cleanupTimeout = 15 * time.Second
)

type probeResult struct {
	readyAt  time.Time
	attempts int
}

// Adapter measures the time between invoking `nerdctl start` and the first
// successful HTTP response from the container.
type Adapter struct {
	readyCh     chan probeResult
	probeCancel context.CancelFunc
	startedAt   time.Time
	url         string
	logPath     string
	cleanupMu   sync.Mutex
}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) ExperimentName() string {
	return "http-readiness"
}

func (a *Adapter) Prepare(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	result, err := runCommandStage(
		ctx,
		harnessruntime.StagePrepare,
		"Pull HTTP readiness image",
		tc,
		"pull", tc.Trial.Image,
	)
	if err != nil {
		err = a.cleanupAfterError(ctx, tc, err)
	}
	return result, err
}

func (a *Adapter) CreateTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	if tc.Trial.Ports == nil {
		err := fmt.Errorf("http-readiness trial %q requires ports", tc.Trial.ID)
		return harnessruntime.StageResult{}, a.cleanupAfterError(ctx, tc, err)
	}

	portMapping := fmt.Sprintf(
		"127.0.0.1:%d:%d",
		tc.Trial.Ports.HostPort,
		tc.Trial.Ports.ContainerPort,
	)

	result, err := runCommandStage(
		ctx,
		harnessruntime.StageCreate,
		"Create HTTP readiness container",
		tc,
		"create",
		"-it",
		"--name", tc.Trial.ID,
		"--runtime", tc.Trial.RuntimeHandler,
		"--publish", portMapping,
		tc.Trial.Image,
	)
	if err != nil {
		err = a.cleanupAfterError(ctx, tc, err)
	}
	return result, err
}

func (a *Adapter) StartTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	if tc.Trial.Ports == nil {
		err := fmt.Errorf("http-readiness trial %q requires ports", tc.Trial.ID)
		return harnessruntime.StageResult{}, a.cleanupAfterError(ctx, tc, err)
	}

	a.url = fmt.Sprintf("http://127.0.0.1:%d/", tc.Trial.Ports.HostPort)
	a.readyCh = make(chan probeResult, 1)

	probeCtx, cancel := context.WithCancel(ctx)
	a.probeCancel = cancel
	startProbe := make(chan time.Time, 1)
	client := &http.Client{Timeout: requestTimeout}
	go probeUntilOK(probeCtx, client, a.url, startProbe, a.readyCh)

	cmd := exec.CommandContext(ctx, "nerdctl", "start", "-a", tc.Trial.ID)
	log.Printf("Running command: %s", cmd.String())

	// temporary log file to capture the server's output
	logFile, err := os.CreateTemp("", "http-readiness-*.log")
	if err != nil {
		result := stageResult(
			harnessruntime.StageStart,
			"Start HTTP readiness container",
			tc,
			a.startedAt,
			time.Now(),
			map[string]interface{}{"url": a.url},
		)
		err = fmt.Errorf("failed to create log file for HTTP readiness server: %w", err)
		return result, a.cleanupAfterError(ctx, tc, err)
	}
	a.logPath = logFile.Name()
	defer logFile.Close()

	cmd.Stdin = os.Stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Detach from the parent's terminal/session.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	a.startedAt = time.Now()
	startProbe <- a.startedAt

	if err := cmd.Start(); err != nil {
		result := stageResult(
			harnessruntime.StageStart,
			"Start HTTP readiness container",
			tc,
			a.startedAt,
			time.Now(),
			map[string]interface{}{"url": a.url},
		)
		err = fmt.Errorf("failed to start HTTP readiness container: %w", err)
		return result, a.cleanupAfterError(ctx, tc, err)
	}

	// Fix: https://github.com/urunc-dev/evaluation_suite/pull/4#discussion_r3721760724
	go func() { _ = cmd.Wait() }()

	finishedAt := time.Now()
	return stageResult(
		harnessruntime.StageStart,
		"Start HTTP readiness container",
		tc,
		a.startedAt,
		finishedAt,
		map[string]interface{}{"url": a.url},
	), nil
}

func (a *Adapter) WaitReady(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	if a.readyCh == nil || a.startedAt.IsZero() {
		err := fmt.Errorf("http readiness probe was not started")
		return harnessruntime.StageResult{}, a.cleanupAfterError(ctx, tc, err)
	}

	select {
	case result := <-a.readyCh:
		if a.probeCancel != nil {
			a.probeCancel()
		}
		return stageResult(
			harnessruntime.StageWaitReady,
			"First HTTP 200 received",
			tc,
			a.startedAt,
			result.readyAt,
			map[string]interface{}{
				"url":                  a.url,
				"status_code":          http.StatusOK,
				"attempts":             result.attempts,
				"ready_at":             result.readyAt,
				"readiness_latency":    result.readyAt.Sub(a.startedAt),
				"readiness_latency_ms": float64(result.readyAt.Sub(a.startedAt).Microseconds()) / 1000,
			},
		), nil
	case <-ctx.Done():
		if a.probeCancel != nil {
			a.probeCancel()
		}
		return harnessruntime.StageResult{}, a.cleanupAfterError(ctx, tc, ctx.Err())
	}
}

func (a *Adapter) Stop(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	if a.probeCancel != nil {
		a.probeCancel()
	}
	result, err := runCommandStage(
		ctx,
		harnessruntime.StageStop,
		"Stop HTTP readiness container",
		tc,
		"stop", tc.Trial.ID,
	)
	if err != nil {
		err = a.cleanupAfterError(ctx, tc, err)
	}
	return result, err
}

func (a *Adapter) DeleteTask(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	result, err := runCommandStage(
		ctx,
		harnessruntime.StageDelete,
		"Delete HTTP readiness container",
		tc,
		"rm", "--force", tc.Trial.ID,
	)
	if err != nil {
		err = a.cleanupAfterError(ctx, tc, err)
	}
	return result, err
}

func (a *Adapter) Cleanup(ctx context.Context, tc harnessruntime.TrialContext) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	err := a.benchmarkCleanup(ctx, tc)
	finishedAt := time.Now()
	return stageResult(
		harnessruntime.StageCleanup,
		"HTTP readiness cleanup complete",
		tc,
		startedAt,
		finishedAt,
		nil,
	), err
}

// benchmarkCleanup releases resources left by a completed or failed trial. It
// is safe to call repeatedly: missing containers and log files are treated as
// already cleaned. All cleanup actions are attempted before errors are returned.
func (a *Adapter) benchmarkCleanup(ctx context.Context, tc harnessruntime.TrialContext) error {
	a.cleanupMu.Lock()
	defer a.cleanupMu.Unlock()

	if a.probeCancel != nil {
		a.probeCancel()
		a.probeCancel = nil
	}

	// A failed stage commonly supplies an already-cancelled context. Preserve
	// its values but give cleanup its own bounded window in which to finish.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	var cleanupErrors []error
	if tc.Trial.ID != "" {
		if err := runCleanupCommand(cleanupCtx, "stop", tc.Trial.ID); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		if err := runCleanupCommand(cleanupCtx, "rm", "--force", tc.Trial.ID); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}

	if a.logPath != "" {
		if err := os.Remove(a.logPath); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove temporary log %q: %w", a.logPath, err))
		} else {
			a.logPath = ""
		}
	}

	return errors.Join(cleanupErrors...)
}

func (a *Adapter) cleanupAfterError(ctx context.Context, tc harnessruntime.TrialContext, stageErr error) error {
	if cleanupErr := a.benchmarkCleanup(ctx, tc); cleanupErr != nil {
		return errors.Join(stageErr, fmt.Errorf("cleanup HTTP readiness benchmark: %w", cleanupErr))
	}
	return stageErr
}

func runCleanupCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "nerdctl", args...)
	log.Printf("Running cleanup command: %s", cmd.String())
	output, err := cmd.CombinedOutput()
	if err == nil || cleanupTargetMissing(output) {
		return nil
	}
	return fmt.Errorf("run cleanup command %q: %w: %s", cmd.String(), err, output)
}

func cleanupTargetMissing(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "no such container") ||
		strings.Contains(message, "container not found") ||
		strings.Contains(message, "does not exist")
}

func probeUntilOK(
	ctx context.Context,
	client *http.Client,
	url string,
	start <-chan time.Time,
	ready chan<- probeResult,
) {
	startedAt, ok := <-start
	if !ok {
		return
	}

	attempts := 0

	for {
		attempts++
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				// client.Do returns after the response headers arrive, so take the
				// timestamp before consuming the body.
				readyAt := time.Now()
				statusCode := response.StatusCode
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if statusCode == http.StatusOK {
					if readyAt.Before(startedAt) {
						readyAt = startedAt
					}
					select {
					case ready <- probeResult{readyAt: readyAt, attempts: attempts}:
					case <-ctx.Done():
					}
					return
				}
			}
		}

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func runCommandStage(
	ctx context.Context,
	stage harnessruntime.Stage,
	description string,
	tc harnessruntime.TrialContext,
	args ...string,
) (harnessruntime.StageResult, error) {
	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, "nerdctl", args...)

	cmd.Stdin = os.Stdin

	log.Printf("Running command: %s", cmd.String())
	output, err := cmd.CombinedOutput()
	finishedAt := time.Now()
	if err != nil {
		return harnessruntime.StageResult{}, fmt.Errorf(
			"run %q: %w: %s",
			cmd.String(),
			err,
			output,
		)
	}

	return stageResult(stage, description, tc, startedAt, finishedAt, nil), nil
}

func stageResult(
	stage harnessruntime.Stage,
	description string,
	tc harnessruntime.TrialContext,
	startedAt time.Time,
	finishedAt time.Time,
	extra map[string]interface{},
) harnessruntime.StageResult {
	latency := finishedAt.Sub(startedAt)
	data := map[string]interface{}{
		"start":      startedAt,
		"end":        finishedAt,
		"latency":    latency,
		"latency_ms": float64(latency.Microseconds()) / 1000,
	}
	for key, value := range extra {
		data[key] = value
	}

	return harnessruntime.StageResult{
		Stage:       stage,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Duration:    latency,
		Description: fmt.Sprintf("%s: trial=%s runtime=%s handler=%s image=%s", description, tc.Trial.ID, tc.Trial.RuntimeName, tc.Trial.RuntimeHandler, tc.Trial.Image),
		Data:        data,
	}
}
