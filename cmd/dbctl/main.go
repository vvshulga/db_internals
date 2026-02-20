// dbctl is the control client for the dbserver daemon.
// It can start, stop, restart, and query the status of the daemon, and
// forwards all storage commands to a running daemon over a Unix socket.
//
// Usage:
//
//	dbctl [--dir <dir>] <command> [args...]
//
// Lifecycle commands:
//
//	start       Start the dbserver daemon in the background.
//	stop        Stop the running daemon.
//	restart     Stop then start.
//	status      Print whether the daemon is running and its PID.
//
// Storage commands (forwarded to the daemon):
//
//	list-tables
//	describe <table>
//	create-table <table> <col:type> [...]
//	drop-table <table>
//	insert <table> <val> [...]
//	get <table> <pageID:slotID>
//	update <table> <pageID:slotID> <val> [...]
//	delete <table> <pageID:slotID>
//	scan <table>
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vvshulga/db_internals/daemon"
)

func usage() {
	fmt.Fprint(os.Stderr, `dbctl — database daemon control tool

Usage: dbctl [--dir <dir>] <command> [args...]

Global flags:
  --dir <path>    database directory (default: ./data)

Lifecycle:
  start              Start the dbserver daemon in the background.
  stop               Stop the running daemon (SIGTERM via PID file).
  restart            Stop then start.
  status             Print whether the daemon is running and its PID.

Storage commands (forwarded to daemon):
  list-tables
  describe <table>
  create-table <table> <col:type> [<col:type> ...]
      Types: int  bigint  float  double  boolean  datetime  varchar(N)  text
      Append ? to mark nullable, e.g. score:double?
  drop-table <table>
  insert <table> <val> [<val> ...]
  get <table> <pageID:slotID>
  update <table> <pageID:slotID> <val> [<val> ...]
  delete <table> <pageID:slotID>
  scan <table>
`)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	dir := flag.String("dir", "./data", "database directory")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	sockPath := filepath.Join(*dir, "dbserver.sock")
	pidPath := filepath.Join(*dir, "dbserver.pid")
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "start":
		doStart(*dir, pidPath, sockPath)
	case "stop":
		doStop(pidPath, sockPath)
	case "restart":
		doStop(pidPath, sockPath)
		doStart(*dir, pidPath, sockPath)
	case "status":
		doStatus(pidPath)
	default:
		// All other commands are forwarded to the daemon.
		doForward(sockPath, cmd, rest)
	}
}

// ---- lifecycle commands -----------------------------------------------------

func doStart(dir, pidPath, sockPath string) {
	// Check if already running.
	if pid, alive := readPID(pidPath); alive {
		fmt.Printf("dbserver already running (pid %d)\n", pid)
		return
	}

	// Find the dbserver binary: look next to this binary first, then $PATH.
	self, _ := os.Executable()
	dbserverPath := filepath.Join(filepath.Dir(self), "dbserver")
	if _, err := os.Stat(dbserverPath); err != nil {
		// Fall back to PATH lookup.
		var lookErr error
		dbserverPath, lookErr = exec.LookPath("dbserver")
		if lookErr != nil {
			fatal("cannot find dbserver binary (looked next to %s and in $PATH)", self)
		}
	}

	// Open log file for daemon output.
	if err := os.MkdirAll(dir, 0755); err != nil {
		fatal("mkdir %s: %v", dir, err)
	}
	logPath := filepath.Join(dir, "dbserver.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fatal("open log file %s: %v", logPath, err)
	}

	// Launch dbserver detached from our process group.
	c := exec.Command(dbserverPath, "--dir", dir)
	c.Stdout = logFile
	c.Stderr = logFile
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		logFile.Close()
		fatal("start dbserver: %v", err)
	}
	logFile.Close()

	// Wait for the socket file to appear (up to 2 seconds).
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(sockPath); err != nil {
		fatal("dbserver did not start within 2s (check %s)", filepath.Join(dir, "dbserver.log"))
	}

	fmt.Printf("dbserver started (pid %d, log: %s)\n", c.Process.Pid, logPath)
}

func doStop(pidPath, sockPath string) {
	pid, alive := readPID(pidPath)
	if !alive {
		fmt.Println("dbserver is not running")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fatal("find process %d: %v", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		fatal("signal %d: %v", pid, err)
	}

	// Wait for PID file to disappear (up to 5 seconds).
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			fmt.Println("dbserver stopped")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Force cleanup of leftover files if the process is gone but files remain.
	if _, stillAlive := readPID(pidPath); !stillAlive {
		_ = os.Remove(pidPath)
		_ = os.Remove(sockPath)
		fmt.Println("dbserver stopped")
		return
	}
	fatal("dbserver did not stop within 5s")
}

func doStatus(pidPath string) {
	pid, alive := readPID(pidPath)
	if !alive {
		fmt.Println("dbserver is not running")
		return
	}
	fmt.Printf("dbserver is running (pid %d)\n", pid)
}

// ---- storage forwarding -----------------------------------------------------

func doForward(sockPath, cmd string, args []string) {
	req := daemon.Request{Cmd: cmd, Args: args}
	resp, err := daemon.Send(sockPath, req)
	if err != nil {
		fatal("connect to daemon: %v\n(is dbserver running? try: dbctl start)", err)
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "error:", resp.Error)
		os.Exit(1)
	}
	if resp.Output != "" {
		fmt.Println(resp.Output)
	}
}

// ---- helpers ----------------------------------------------------------------

// readPID reads the PID file and returns (pid, true) if the process is alive,
// or (0, false) otherwise.
func readPID(pidPath string) (int, bool) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	// Send signal 0 to test liveness without killing the process.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}
