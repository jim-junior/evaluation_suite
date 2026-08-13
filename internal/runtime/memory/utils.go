package memory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func readCgroupPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read container cgroup: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			return parts[2], nil
		}
	}
	return "", errors.New("cgroup v2 entry not found")
}

func readCgroupMetrics(dir string) (CgroupMetrics, error) {
	current, err := readUintFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return CgroupMetrics{}, err
	}
	peak, err := readUintFile(filepath.Join(dir, "memory.peak"))
	if err != nil {
		return CgroupMetrics{}, err
	}
	stat, err := readKeyValueFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return CgroupMetrics{}, err
	}
	return CgroupMetrics{CurrentBytes: current, PeakBytes: peak, Stat: stat}, nil
}

func readUintFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}

func readKeyValueFile(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	values := make(map[string]uint64)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		if len(fields) < 2 {
			continue
		}

		// smaps_rollup starts with a VMA header such as:
		// c000000000-7ffc4b9a9000 ---p 00000000 00:00 0 [rollup]
		if !strings.HasSuffix(fields[0], ":") {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"parse %s value %q for key %q: %w",
				path,
				fields[1],
				fields[0],
				err,
			)
		}

		key := strings.TrimSuffix(fields[0], ":")
		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return values, nil
}

func findShimPID(procRoot, containerID string) (int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, fmt.Errorf("read proc: %w", err)
	}

	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err == nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)

	for _, pid := range pids {
		data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
		if err != nil {
			continue // Processes can exit while /proc is being scanned.
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(args) == 0 || !strings.Contains(filepath.Base(args[0]), "containerd-shim") {
			continue
		}
		for _, arg := range args[1:] {
			if arg == containerID {
				return pid, nil
			}
		}
	}

	return 0, fmt.Errorf("containerd shim for container %s not found", containerID)
}

func readShimMetrics(path string, pid int) (ShimMetrics, error) {
	values, err := readKeyValueFile(path)
	if err != nil {
		return ShimMetrics{}, err
	}
	return ShimMetrics{
		PID:      pid,
		PSSBytes: values["Pss"] * 1024,
		USSBytes: (values["Private_Clean"] + values["Private_Dirty"]) * 1024,
		RSSBytes: values["Rss"] * 1024,
	}, nil
}

func pullImage(ctx context.Context, image string) error {
	_, err := runNerdctl(ctx, "pull", image)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}
