package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/vvshulga/db_internals/internal/cliutil"
	"github.com/vvshulga/db_internals/storage"
)

// Server accepts client connections on a Unix socket and dispatches storage
// commands against a single shared storage.DB instance.
type Server struct {
	db       *storage.DB
	listener net.Listener
	wg       sync.WaitGroup
}

// NewServer creates a Server that uses the given listener and database.
func NewServer(l net.Listener, db *storage.DB) *Server {
	return &Server{db: db, listener: l}
}

// Serve runs the accept loop, blocking until the listener is closed.
// Each accepted connection is handled in its own goroutine.
func (s *Server) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener was closed; stop accepting.
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Shutdown closes the listener and waits for all in-flight connections to finish.
func (s *Server) Shutdown() {
	s.listener.Close()
	s.wg.Wait()
}

// handleConn reads one JSON request from conn, dispatches it, writes the JSON
// response, and closes the connection.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	var req Request
	dec := json.NewDecoder(bufio.NewReader(conn))
	if err := dec.Decode(&req); err != nil {
		resp := Response{OK: false, Error: fmt.Sprintf("decode request: %v", err)}
		_ = json.NewEncoder(conn).Encode(resp)
		return
	}

	resp := s.dispatch(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

// dispatch routes a request to the appropriate handler and returns a Response.
func (s *Server) dispatch(req Request) Response {
	switch req.Cmd {
	case "shutdown":
		// The caller (dbserver main) handles the actual shutdown via signal.
		// We acknowledge here; the process will exit after Serve returns.
		go s.Shutdown()
		return Response{OK: true, Output: "shutting down"}

	case "list-tables":
		return s.doListTables()

	case "describe":
		if len(req.Args) != 1 {
			return errResp("describe: usage: describe <table>")
		}
		return s.doDescribe(req.Args[0])

	case "create-table":
		if len(req.Args) < 2 {
			return errResp("create-table: usage: create-table <table> <col:type> [...]")
		}
		return s.doCreateTable(req.Args[0], req.Args[1:])

	case "drop-table":
		if len(req.Args) != 1 {
			return errResp("drop-table: usage: drop-table <table>")
		}
		return s.doDropTable(req.Args[0])

	case "insert":
		if len(req.Args) < 1 {
			return errResp("insert: usage: insert <table> <val> [...]")
		}
		return s.doInsert(req.Args[0], req.Args[1:])

	case "get":
		if len(req.Args) != 2 {
			return errResp("get: usage: get <table> <pageID:slotID>")
		}
		return s.doGet(req.Args[0], req.Args[1])

	case "update":
		if len(req.Args) < 2 {
			return errResp("update: usage: update <table> <pageID:slotID> <val> [...]")
		}
		return s.doUpdate(req.Args[0], req.Args[1], req.Args[2:])

	case "delete":
		if len(req.Args) != 2 {
			return errResp("delete: usage: delete <table> <pageID:slotID>")
		}
		return s.doDelete(req.Args[0], req.Args[1])

	case "scan":
		if len(req.Args) != 1 {
			return errResp("scan: usage: scan <table>")
		}
		return s.doScan(req.Args[0])

	default:
		return errResp(fmt.Sprintf("unknown command %q", req.Cmd))
	}
}

func errResp(msg string) Response { return Response{OK: false, Error: msg} }

// ---- command handlers -------------------------------------------------------

func (s *Server) doListTables() Response {
	names := s.db.TableNames()
	return Response{OK: true, Output: strings.Join(names, "\n")}
}

func (s *Server) doDescribe(table string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	schema := tbl.Schema()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-20s  %-16s  %s\n", "Column", "Type", "Nullable")
	fmt.Fprintf(&sb, "%s  %s  %s\n",
		strings.Repeat("-", 20), strings.Repeat("-", 16), strings.Repeat("-", 8))
	for i := 0; i < schema.NumColumns(); i++ {
		col := schema.Column(i)
		fmt.Fprintf(&sb, "%-20s  %-16s  %v\n", col.Name, cliutil.FormatType(col), col.Nullable)
	}
	return Response{OK: true, Output: strings.TrimRight(sb.String(), "\n")}
}

func (s *Server) doCreateTable(name string, specs []string) Response {
	cols := make([]storage.Column, len(specs))
	for i, spec := range specs {
		col, err := cliutil.ParseColSpec(spec)
		if err != nil {
			return errResp(err.Error())
		}
		cols[i] = col
	}
	schema, err := storage.NewSchema(cols)
	if err != nil {
		return errResp(err.Error())
	}
	if _, err := s.db.CreateTable(name, schema); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true, Output: fmt.Sprintf("table %q created", name)}
}

func (s *Server) doDropTable(name string) Response {
	if err := s.db.DropTable(name); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true, Output: fmt.Sprintf("table %q dropped", name)}
}

func (s *Server) doInsert(table string, vals []string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	row, err := cliutil.ParseValues(tbl.Schema(), vals)
	if err != nil {
		return errResp(err.Error())
	}
	rid, err := tbl.Insert(row)
	if err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true, Output: "inserted " + rid.String()}
}

func (s *Server) doGet(table, ridStr string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	rid, err := cliutil.ParseRID(ridStr)
	if err != nil {
		return errResp(err.Error())
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return Response{OK: true, Output: "not found"}
	}
	return Response{OK: true, Output: rid.String() + "  " + cliutil.FormatRow(tbl.Schema(), row)}
}

func (s *Server) doUpdate(table, ridStr string, vals []string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	rid, err := cliutil.ParseRID(ridStr)
	if err != nil {
		return errResp(err.Error())
	}
	row, err := cliutil.ParseValues(tbl.Schema(), vals)
	if err != nil {
		return errResp(err.Error())
	}
	newRID, ok, err := tbl.Update(rid, row)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return Response{OK: true, Output: "not found"}
	}
	return Response{OK: true, Output: "updated " + newRID.String()}
}

func (s *Server) doDelete(table, ridStr string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	rid, err := cliutil.ParseRID(ridStr)
	if err != nil {
		return errResp(err.Error())
	}
	ok, err := tbl.Delete(rid)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return Response{OK: true, Output: "not found"}
	}
	return Response{OK: true, Output: "deleted"}
}

func (s *Server) doScan(table string) Response {
	tbl, err := s.db.OpenTable(table)
	if err != nil {
		return errResp(err.Error())
	}
	var sb strings.Builder
	sc := tbl.Scan()
	for sc.Next() {
		fmt.Fprintf(&sb, "%s  %s\n", sc.RID(), cliutil.FormatRow(tbl.Schema(), sc.Row()))
	}
	if err := sc.Err(); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true, Output: strings.TrimRight(sb.String(), "\n")}
}
