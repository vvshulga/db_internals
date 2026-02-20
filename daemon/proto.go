// Package daemon provides the wire protocol and client/server components for
// the dbserver daemon. Communication uses newline-delimited JSON over a Unix
// domain socket.
package daemon

// Request is a command sent from a client to the daemon.
type Request struct {
	Cmd  string   `json:"cmd"`            // command name, e.g. "insert"
	Args []string `json:"args,omitempty"` // positional arguments
}

// Response is the daemon's reply to a Request.
type Response struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"` // human-readable output on success
	Error  string `json:"error,omitempty"`  // error message on failure
}
