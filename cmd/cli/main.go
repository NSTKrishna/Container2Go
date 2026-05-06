package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"Container2Go/internal/runtime"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runContainer()
	case "child":
		runtime.RunChild(os.Args[2:])
	case "ps":
		listContainers()
	case "stop":
		stopContainer()
	case "logs":
		showLogs()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runContainer() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker-cli run <command> [args...]\n")
		os.Exit(1)
	}

	userCmd := strings.Join(os.Args[2:], " ")
	id := generateID()

	containerDir := runtime.ContainerPath(id)
	os.MkdirAll(containerDir, 0755)

	logPath := filepath.Join(containerDir, "logs.txt")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create log file: %v\n", err)
		os.Exit(1)
	}

	childArgs := append([]string{"child", id, runtime.TemplateRootFS}, os.Args[2:]...)
	cmd := exec.Command("/proc/self/exe", childArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		fmt.Fprintf(os.Stderr, "Failed to start container: %v\n", err)
		os.Exit(1)
	}

	c := &runtime.Container{
		ID:      id,
		PID:     cmd.Process.Pid,
		Command: userCmd,
		Status:  "running",
		RootFS:  runtime.TemplateRootFS,
		LogFile: logPath,
	}
	runtime.SaveMetadata(c)

	go func() {
		cmd.Wait()
		logFile.Close()
		c, _ := runtime.LoadMetadata(id)
		if c != nil && c.Status == "running" {
			c.Status = "exited"
			runtime.SaveMetadata(c)
		}
	}()

	fmt.Printf("Container %s started (PID: %d)\n", id, cmd.Process.Pid)
}

func listContainers() {
	containers, err := runtime.ListContainers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(containers) == 0 {
		fmt.Println("No containers found.")
		return
	}

	for _, c := range containers {
		if c.Status == "running" {
			if err := syscall.Kill(c.PID, 0); err != nil {
				c.Status = "exited"
				runtime.SaveMetadata(c)
			}
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "ID\tPID\tSTATUS\tUSER\tCOMMAND")
	for _, c := range containers {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", c.ID, c.PID, c.Status, c.UserID, c.Command)
	}
	w.Flush()
}

func stopContainer() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker-cli stop <container-id>\n")
		os.Exit(1)
	}
	id := os.Args[2]
	c, err := runtime.LoadMetadata(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Container %s not found\n", id)
		os.Exit(1)
	}
	if c.Status != "running" {
		fmt.Printf("Container %s is %s\n", id, c.Status)
		return
	}
	syscall.Kill(c.PID, syscall.SIGKILL)
	c.Status = "stopped"
	runtime.SaveMetadata(c)
	fmt.Printf("Container %s stopped.\n", id)
}

func showLogs() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker-cli logs <container-id>\n")
		os.Exit(1)
	}
	logPath := filepath.Join(runtime.ContainerPath(os.Args[2]), "logs.txt")
	data, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Logs not found for container %s\n", os.Args[2])
		os.Exit(1)
	}
	if len(data) == 0 {
		fmt.Println("No logs available.")
		return
	}
	fmt.Print(string(data))
}

func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func printUsage() {
	fmt.Println("MiniDocker CLI — Standalone container runtime")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  minidocker-cli run <command> [args...]   Start a container")
	fmt.Println("  minidocker-cli ps                        List containers")
	fmt.Println("  minidocker-cli stop <container-id>       Stop a container")
	fmt.Println("  minidocker-cli logs <container-id>       View container logs")
}
