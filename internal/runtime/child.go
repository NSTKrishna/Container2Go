package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// RunChild is the entry point for the containerized child process.
//
// This function is called when the binary is re-executed with the "child"
// argument. At this point, the process is ALREADY running inside new Linux
// namespaces (UTS, PID, Mount) created by the parent via clone flags.
//
// The process hierarchy:
//
//   Server (host PID namespace)
//     └── Child process (new PID namespace — this function)
//           └── /bin/bash (user shell, PID 2 in container)
//
// Setup steps performed here:
//   1. Set hostname to "container" (isolated by UTS namespace)
//   2. chroot to the user's rootfs directory
//   3. Mount /proc (virtual filesystem for process info)
//   4. Mount tmpfs at /mytemp (in-memory scratch space)
//   5. Configure cgroups for resource limits
//   6. Execute the user's command (e.g., /bin/bash)
//   7. Clean up mounts on exit
//
// The child's stdin/stdout/stderr are inherited from the parent. When the
// parent uses PTY allocation, these will be the PTY slave — giving the
// user an interactive terminal with tab completion, signal handling, etc.
//
// Args format: [containerID, rootfsPath, command, args...]
func RunChild(args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "child: need containerID, rootfsPath, command\n")
		os.Exit(1)
	}

	containerID := args[0]
	rootfsPath := args[1]
	userCmd := args[2]
	userArgs := args[3:]

	// Build the user's command. It inherits our stdio (which may be a PTY slave).
	cmd := exec.Command(userCmd, userArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// --- UTS Namespace ---
	// Set a custom hostname visible only inside this container.
	// The UTS (Unix Timesharing System) namespace isolates hostname and
	// NIS domain name, so each container can have its own hostname.
	must(syscall.Sethostname([]byte("container")))

	// --- Chroot ---
	// Change the root filesystem to the user's dedicated directory.
	// After chroot, "/" points to rootfsPath. The process cannot access
	// any files outside this directory tree — this is filesystem isolation.
	must(syscall.Chroot(rootfsPath))
	must(os.Chdir("/"))

	// --- Mount /proc ---
	// /proc is a virtual filesystem provided by the Linux kernel.
	// It exposes process and system information as files:
	//   /proc/1/status  → info about PID 1
	//   /proc/cpuinfo   → CPU information
	//   /proc/meminfo   → memory information
	// Without /proc, commands like ps, top, kill won't work.
	// Because we're in a PID namespace, /proc only shows container processes.
	os.MkdirAll("/proc", 0755)
	must(syscall.Mount("proc", "/proc", "proc", 0, ""))

	// --- Mount tmpfs ---
	// tmpfs is a temporary filesystem stored entirely in RAM.
	// It's fast, and all data disappears when the container exits.
	// Useful for scratch space, temporary files, or /tmp.
	os.MkdirAll("/mytemp", 0755)
	must(syscall.Mount("tmpfs", "/mytemp", "tmpfs", 0, ""))

	// --- Cgroups ---
	// Set resource limits for this container.
	SetupCgroup(containerID)

	// --- Execute user command ---
	err := cmd.Run()

	// --- Cleanup mounts ---
	// Always unmount, even if the command failed, to avoid stale mounts.
	syscall.Unmount("/proc", 0)
	syscall.Unmount("/mytemp", 0)

	if err != nil {
		fmt.Fprintf(os.Stderr, "container command exited: %v\n", err)
		os.Exit(1)
	}
}
