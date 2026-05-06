package runtime

import (
	"os"
	"path/filepath"
	"strconv"
)

// SetupCgroup configures cgroup resource limits for a container.
//
// Linux cgroups (control groups) allow the kernel to limit, account for,
// and isolate resource usage of a collection of processes.
//
// We set up TWO cgroup controllers:
//
// 1. PIDs controller — limits the maximum number of processes.
//    This prevents fork bombs from consuming all host PIDs.
//    Files:
//      pids.max          → max processes (set to 20)
//      notify_on_release → auto-cleanup when all processes exit
//      cgroup.procs      → assigns current process to this cgroup
//
// 2. Memory controller — limits memory usage (if available).
//    This prevents a single container from exhausting host RAM.
//    Files:
//      memory.limit_in_bytes → max memory in bytes (set to 100MB)
//
// Each container gets its own cgroup directory named "minidocker_<id>".
func SetupCgroup(containerID string) {
	cgroupBase := "/sys/fs/cgroup/"

	// --- PID limits ---
	pidsDir := filepath.Join(cgroupBase, "pids", "minidocker_"+containerID)
	os.MkdirAll(pidsDir, 0755)

	must(os.WriteFile(filepath.Join(pidsDir, "pids.max"), []byte("20"), 0700))
	must(os.WriteFile(filepath.Join(pidsDir, "notify_on_release"), []byte("1"), 0700))
	must(os.WriteFile(filepath.Join(pidsDir, "cgroup.procs"),
		[]byte(strconv.Itoa(os.Getpid())), 0700))

	// --- Memory limits (best-effort, may not exist on all systems) ---
	memDir := filepath.Join(cgroupBase, "memory", "minidocker_"+containerID)
	if err := os.MkdirAll(memDir, 0755); err == nil {
		// 100MB memory limit
		os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"),
			[]byte("104857600"), 0700)
		os.WriteFile(filepath.Join(memDir, "cgroup.procs"),
			[]byte(strconv.Itoa(os.Getpid())), 0700)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
