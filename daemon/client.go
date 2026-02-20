package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Send dials the Unix socket at sockPath, sends req as a JSON line, reads the
// response JSON line, and returns it. The connection is closed after one
// request/response exchange.
func Send(sockPath string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("dial %s: %w", sockPath, err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return Response{}, fmt.Errorf("send request: %w", err)
	}

	var resp Response
	dec := json.NewDecoder(bufio.NewReader(conn))
	if err := dec.Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}
