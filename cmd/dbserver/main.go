// dbserver is the long-running database daemon. It opens a storage.DB,
// listens on a Unix socket, and serves storage commands to dbctl clients.
//
// Usage:
//
//	dbserver [--dir <dir>] [--sock <path>] [--pid <path>]
//
// The daemon writes its PID to <dir>/dbserver.pid and creates a Unix socket at
// <dir>/dbserver.sock. Both files are removed on clean exit.
// Send SIGTERM or SIGINT to trigger a graceful shutdown.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/vvshulga/db_internals/daemon"
	"github.com/vvshulga/db_internals/storage"
)

func main() {
	dir := flag.String("dir", "./data", "database directory")
	sock := flag.String("sock", "", "unix socket path (default: <dir>/dbserver.sock)")
	pid := flag.String("pid", "", "PID file path (default: <dir>/dbserver.pid)")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", *dir, err)
	}

	sockPath := *sock
	if sockPath == "" {
		sockPath = filepath.Join(*dir, "dbserver.sock")
	}
	pidPath := *pid
	if pidPath == "" {
		pidPath = filepath.Join(*dir, "dbserver.pid")
	}

	// Write PID file so dbctl can manage this process.
	pidData := []byte(strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(pidPath, pidData, 0644); err != nil {
		log.Fatalf("write pid file %s: %v", pidPath, err)
	}

	// Open the database.
	db, err := storage.OpenDB(*dir)
	if err != nil {
		_ = os.Remove(pidPath)
		log.Fatalf("open db %s: %v", *dir, err)
	}

	// Remove stale socket file before listening.
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(pidPath)
		log.Fatalf("listen %s: %v", sockPath, err)
	}

	srv := daemon.NewServer(ln, db)

	// Handle SIGTERM and SIGINT for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "dbserver: received %v, shutting down\n", sig)
		srv.Shutdown()
	}()

	fmt.Fprintf(os.Stderr, "dbserver: listening on %s (pid %d)\n", sockPath, os.Getpid())
	srv.Serve() // blocks until Shutdown is called

	// Cleanup.
	if err := db.Close(); err != nil {
		log.Printf("db close: %v", err)
	}
	_ = os.Remove(sockPath)
	_ = os.Remove(pidPath)
	fmt.Fprintln(os.Stderr, "dbserver: stopped")
}
