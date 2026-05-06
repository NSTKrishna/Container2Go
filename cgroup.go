package main

import (
	"os"
	"path/filepath"
	"strconv"
)

// cg sets up a cgroup to limit the number of processes (PIDs) a container can create.
//
// Linux cgroups (control groups) allow the kernel to limit, account for, and isolate
// resource usage (CPU, memory, disk I/O, PIDs, etc.) of a collection of processes.
//
// Here we use the "pids" cgroup controller to cap the maximum number of processes
// inside the container to 20. This prevents fork bombs and runaway process creation.
//
// The containerID parameter creates a per-container cgroup directory so that
// multiple containers don't interfere with each other's resource limits.
//
// Key files:
//   - pids.max:           Maximum number of PIDs allowed in this cgroup
//   - notify_on_release:  If set to 1, the kernel will clean up the cgroup directory
//                         when all processes in it have exited
//   - cgroup.procs:       Writing a PID here moves that process into this cgroup
func cg(containerID string) {
	cgroups := "/sys/fs/cgroup/"
	pids := filepath.Join(cgroups, "pids")

	// Create a cgroup directory named after the container ID
	cgroupDir := filepath.Join(pids, "minidocker_"+containerID)
	os.MkdirAll(cgroupDir, 0755)

	// Limit to 20 processes inside this container
	must(os.WriteFile(filepath.Join(cgroupDir, "pids.max"), []byte("20"), 0700))

	// Auto-cleanup: remove the cgroup directory when the last process exits
	must(os.WriteFile(filepath.Join(cgroupDir, "notify_on_release"), []byte("1"), 0700))

	// Move the current process (and all its future children) into this cgroup
	must(os.WriteFile(filepath.Join(cgroupDir, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0700))
}
