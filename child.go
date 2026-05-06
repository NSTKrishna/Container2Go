package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// child is the entry point for the containerized child process.
//
// When "minidocker run" is called, the parent process re-executes itself with
// the "child" argument. This child process runs inside new Linux namespaces
// (UTS, PID, Mount) created by the parent via clone flags.
//
// The child process performs the following setup before running the user's command:
//   1. Set a custom hostname ("container") — isolated by UTS namespace
//   2. Change the root filesystem (chroot) to an Ubuntu rootfs
//   3. Mount /proc — required for process listing inside the container
//   4. Mount a tmpfs at /mytemp — a temporary in-memory filesystem
//   5. Set up cgroups to limit process count
//   6. Execute the user's command
//   7. Clean up mounts on exit
//
// Args format: child <container-id> <command> [args...]
func child() {
	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "child: insufficient arguments\n")
		os.Exit(1)
	}

	containerID := os.Args[2]
	userCmd := os.Args[3]
	userArgs := os.Args[4:]

	fmt.Printf("Running %v \n", append([]string{userCmd}, userArgs...))

	cmd := exec.Command(userCmd, userArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// --- UTS Namespace: Set a custom hostname visible only inside the container ---
	must(syscall.Sethostname([]byte("container")))

	// --- Chroot: Change the root filesystem ---
	// This makes /home/ubuntu/ubuntufs appear as "/" inside the container.
	// The container cannot see or access files outside this directory.
	must(syscall.Chroot("/home/ubuntu/ubuntufs"))
	must(os.Chdir("/"))

	// --- Mount Setup ---
	// /proc is a virtual filesystem that exposes kernel and process information.
	// Without mounting /proc, commands like `ps`, `top`, and `kill` won't work.
	os.MkdirAll("/proc", 0755)
	os.MkdirAll("/mytemp", 0755)

	must(syscall.Mount("proc", "/proc", "proc", 0, ""))

	// tmpfs is a temporary filesystem stored in RAM. It's fast and disappears
	// when the container exits — useful for scratch space.
	must(syscall.Mount("tmpfs", "/mytemp", "tmpfs", 0, ""))

	// --- Cgroups: Limit resources ---
	cg(containerID)

	// --- Run the user's command ---
	err := cmd.Run()

	// --- Cleanup: Unmount filesystems ---
	// We unmount even if the command failed, to avoid leaving stale mounts.
	syscall.Unmount("/proc", 0)
	syscall.Unmount("/mytemp", 0)

	if err != nil {
		// Update container status to "exited" before exiting
		updateStatusOnExit(containerID)
		fmt.Fprintf(os.Stderr, "command exited with error: %v\n", err)
		os.Exit(1)
	}

	updateStatusOnExit(containerID)
}

// updateStatusOnExit is a no-op inside the chrooted child process.
// Since we're inside chroot, we can't access /tmp/minidocker on the host.
// Status updates are handled by:
//   - The run command's background goroutine (waits for child to exit)
//   - The ps command (probes PID liveness with syscall.Kill(pid, 0))
func updateStatusOnExit(containerID string) {
	_ = containerID
}
