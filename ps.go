package main

import (
	"fmt"
	"os"
	"syscall"
	"text/tabwriter"
)

// ps lists all containers and their current status.
//
// How it works:
//   1. Scan /tmp/minidocker/ for container directories
//   2. Load each container's config.json metadata
//   3. For containers with status "running", verify the process is still alive
//      by sending signal 0 to the PID using syscall.Kill(pid, 0)
//      - Signal 0 doesn't actually send a signal — it just checks if the
//        process exists and we have permission to signal it
//      - If the process is dead (error returned), update status to "exited"
//   4. Print a formatted table with ID, PID, STATUS, and COMMAND columns
func ps() {
	containers, err := ListContainers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing containers: %v\n", err)
		os.Exit(1)
	}

	if len(containers) == 0 {
		fmt.Println("No containers found.")
		return
	}

	// Check if "running" containers are actually still alive
	for _, c := range containers {
		if c.Status == "running" {
			// syscall.Kill with signal 0 is a standard UNIX technique to check
			// if a process is alive. It returns nil if the process exists.
			err := syscall.Kill(c.PID, 0)
			if err != nil {
				// Process is dead — update status to "exited"
				c.Status = "exited"
				SaveMetadata(c)
			}
		}
	}

	// Print a nicely formatted table using tabwriter.
	// tabwriter aligns columns with elastic tab stops for clean output.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "ID\tPID\tSTATUS\tCOMMAND")
	for _, c := range containers {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", c.ID, c.PID, c.Status, c.Command)
	}
	w.Flush()
}
