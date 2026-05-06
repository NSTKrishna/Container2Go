package main

import (
	"fmt"
	"os"
)

// MiniDocker — A minimal container runtime with lifecycle management.
//
// This is an educational container runtime that demonstrates:
//   - Linux namespaces (UTS, PID, Mount) for process isolation
//   - chroot for filesystem isolation
//   - cgroups for resource limiting (PID count)
//   - proc and tmpfs mounting inside containers
//   - Container lifecycle: run, ps, stop, logs
//
// Usage:
//   minidocker run <command> [args...]   — Start a new container
//   minidocker ps                        — List all containers
//   minidocker stop <container-id>       — Stop a running container
//   minidocker logs <container-id>       — View container logs
//
// Requirements:
//   - Linux with root privileges
//   - A root filesystem at /home/ubuntu/ubuntufs
func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		// Internal command — called by run() to set up the container.
		// This is not meant to be called directly by the user.
		child()
	case "ps":
		ps()
	case "stop":
		stop()
	case "logs":
		logs()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// printUsage displays the help message with all available commands.
func printUsage() {
	fmt.Println("MiniDocker — A minimal container runtime")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  minidocker run <command> [args...]   Start a new container")
	fmt.Println("  minidocker ps                        List all containers")
	fmt.Println("  minidocker stop <container-id>       Stop a running container")
	fmt.Println("  minidocker logs <container-id>       View container logs")
}