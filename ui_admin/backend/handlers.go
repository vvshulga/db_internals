package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/query"
	"github.com/vvshulga/db_internals/storage"
)

// Server holds the database and HTTP router
type Server struct {
	db        *storage.DB
	parentDir string     // parent of db.Dir() — where sibling databases live
	mu        sync.Mutex // guards db swaps
	mux       *http.ServeMux
}

// NewServer creates a new HTTP server with routes
func NewServer(db *storage.DB) *Server {
	s := &Server{
		db:        db,
		parentDir: filepath.Dir(db.Dir()),
		mux:       http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// switchDatabase closes the current DB and opens the one at dir.
func (s *Server) switchDatabase(dir string) error {
	newDB, err := storage.OpenDB(dir)
	if err != nil {
		return fmt.Errorf("cannot open database: %w", err)
	}
	s.mu.Lock()
	old := s.db
	s.db = newDB
	s.mu.Unlock()
	return old.Close()
}

// Router returns the HTTP handler
func (s *Server) Router() http.Handler {
	return s.mux
}

// registerRoutes sets up all HTTP routes
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/info", s.handleGetInfo)
	s.mux.HandleFunc("/api/databases", s.handleDatabases)
	s.mux.HandleFunc("/api/db/switch", s.handleSwitchDB)
	s.mux.HandleFunc("/api/tables", s.handleTables)
	s.mux.HandleFunc("/api/tables/", s.handleTableOps)
	s.mux.HandleFunc("/api/query", s.handleQuery)
}

// handleGetInfo returns database metadata
func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	tableNames := s.db.TableNames()

	resp := InfoResponse{
		DatabaseDir: s.db.Dir(),
		CurrentDB:   filepath.Base(s.db.Dir()),
		TableCount:  len(tableNames),
		TableNames:  tableNames,
	}

	respondJSON(w, http.StatusOK, resp)
}

// handleDatabases returns the list of all databases in the parent directory (GET only).
func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	names, err := storage.ListDatabases(s.parentDir)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list databases: %v", err))
		return
	}
	if names == nil {
		names = []string{}
	}
	respondJSON(w, http.StatusOK, names)
}

// handleSwitchDB switches the active database (POST only).
func (s *Server) handleSwitchDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req SwitchDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}
	if req.Name == "" || strings.ContainsAny(req.Name, "/\\") {
		respondError(w, http.StatusBadRequest, "Invalid database name")
		return
	}
	newDir := filepath.Join(s.parentDir, req.Name)
	if err := s.switchDatabase(newDir); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: fmt.Sprintf("Switched to database '%s'", req.Name),
	})
}

// handleTables handles GET (list tables) and POST (create table)
func (s *Server) handleTables(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.handleListTables(w, r)
	case "POST":
		s.handleCreateTable(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleListTables returns list of all tables
func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	tableNames := s.db.TableNames()
	respondJSON(w, http.StatusOK, tableNames)
}

// handleCreateTable creates a new table
func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	var req CreateTableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Validate request
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "Table name is required")
		return
	}
	if len(req.Columns) == 0 {
		respondError(w, http.StatusBadRequest, "At least one column is required")
		return
	}

	// Build schema
	cols := make([]storage.Column, len(req.Columns))
	for i, col := range req.Columns {
		if col.Name == "" {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("Column %d has empty name", i))
			return
		}

		dataType, maxLen, err := parseColumnType(col.Type)
		if err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("Column %q: %v", col.Name, err))
			return
		}

		cols[i] = storage.Column{
			Name:     col.Name,
			Type:     dataType,
			MaxLen:   maxLen,
			Nullable: col.Nullable,
		}
	}

	schema, err := storage.NewSchema(cols)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid schema: %v", err))
		return
	}

	// Create table
	if _, err := s.db.CreateTable(req.Name, schema); err != nil {
		var tableExists *storage.ErrTableExists
		if errors.As(err, &tableExists) {
			respondError(w, http.StatusConflict, fmt.Sprintf("Table %q already exists", req.Name))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create table: %v", err))
		return
	}

	respondJSON(w, http.StatusCreated, SuccessResponse{
		Success: true,
		Message: fmt.Sprintf("Table %q created", req.Name),
	})
}

// handleTableOps routes table-specific operations
func (s *Server) handleTableOps(w http.ResponseWriter, r *http.Request) {
	// Parse table name from path: /api/tables/{name}[/rows[/{rid}]]
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tables/"), "/")
	if len(pathParts) == 0 || pathParts[0] == "" {
		respondError(w, http.StatusBadRequest, "Table name required")
		return
	}

	tableName := pathParts[0]

	// Route based on path structure
	if len(pathParts) == 1 {
		// /api/tables/{name}
		switch r.Method {
		case "GET":
			s.handleGetTableSchema(w, r, tableName)
		case "DELETE":
			s.handleDropTable(w, r, tableName)
		default:
			respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	if len(pathParts) >= 2 && pathParts[1] == "indexes" {
		// /api/tables/{name}/indexes          POST → create index
		// /api/tables/{name}/indexes/{column} DELETE → drop index
		if len(pathParts) == 2 {
			if r.Method == "POST" {
				s.handleCreateIndex(w, r, tableName)
			} else {
				respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}
		if len(pathParts) == 3 {
			colName := pathParts[2]
			if r.Method == "DELETE" {
				s.handleDropIndex(w, r, tableName, colName)
			} else {
				respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}
	}

	if len(pathParts) >= 2 && pathParts[1] == "rows" {
		// /api/tables/{name}/rows[/{rid}]
		if len(pathParts) == 2 {
			// /api/tables/{name}/rows
			switch r.Method {
			case "GET":
				s.handleScanRows(w, r, tableName)
			case "POST":
				s.handleInsertRow(w, r, tableName)
			default:
				respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}

		if len(pathParts) == 3 {
			// /api/tables/{name}/rows/{rid}
			rid := pathParts[2]
			switch r.Method {
			case "GET":
				s.handleGetRow(w, r, tableName, rid)
			case "PUT":
				s.handleUpdateRow(w, r, tableName, rid)
			case "DELETE":
				s.handleDeleteRow(w, r, tableName, rid)
			default:
				respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}
	}

	respondError(w, http.StatusNotFound, "Endpoint not found")
}

// handleGetTableSchema returns table schema including index metadata.
func (s *Server) handleGetTableSchema(w http.ResponseWriter, r *http.Request, tableName string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, SchemaToJSONWithIndexes(table))
}

// handleDropTable deletes a table
func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request, tableName string) {
	if err := s.db.DropTable(tableName); err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to drop table: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: fmt.Sprintf("Table %q dropped", tableName),
	})
}

// handleCreateIndex creates a B-tree index on a column: POST /api/tables/{name}/indexes
func (s *Server) handleCreateIndex(w http.ResponseWriter, r *http.Request, tableName string) {
	var req CreateIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}
	if req.Column == "" {
		respondError(w, http.StatusBadRequest, "column is required")
		return
	}
	tbl, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}
	if err := tbl.CreateIndex(req.Column, req.Unique); err != nil {
		var idxExists *storage.ErrIndexExists
		if errors.As(err, &idxExists) {
			respondError(w, http.StatusConflict, fmt.Sprintf("Index on %q already exists", req.Column))
			return
		}
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Failed to create index: %v", err))
		return
	}
	kind := "Index"
	if req.Unique {
		kind = "Unique index"
	}
	respondJSON(w, http.StatusCreated, SuccessResponse{
		Success: true,
		Message: fmt.Sprintf("%s on column %q created", kind, req.Column),
	})
}

// handleDropIndex removes a column index: DELETE /api/tables/{name}/indexes/{column}
func (s *Server) handleDropIndex(w http.ResponseWriter, r *http.Request, tableName, colName string) {
	tbl, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}
	if err := tbl.DropIndex(colName); err != nil {
		var idxNotFound *storage.ErrIndexNotFound
		if errors.As(err, &idxNotFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("No index on column %q", colName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to drop index: %v", err))
		return
	}
	respondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: fmt.Sprintf("Index on column %q dropped", colName),
	})
}

// handleScanRows returns all rows from a table (with pagination support)
func (s *Server) handleScanRows(w http.ResponseWriter, r *http.Request, tableName string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	// Parse pagination parameters (optional)
	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")

	page := 1
	pageSize := 1000 // default: return all rows (up to 1000)

	if pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err != nil || p < 1 {
			respondError(w, http.StatusBadRequest, "Invalid page number")
			return
		}
		page = p
	}

	if pageSizeStr != "" {
		ps, err := strconv.Atoi(pageSizeStr)
		if err != nil || ps < 1 || ps > 1000 {
			respondError(w, http.StatusBadRequest, "Invalid page_size (must be 1-1000)")
			return
		}
		pageSize = ps
	}

	// Scan all rows
	scanner := table.Scan()
	rows := []RowData{}
	totalCount := 0

	for scanner.Next() {
		totalCount++
		// Simple pagination: skip rows before current page
		if totalCount < (page-1)*pageSize+1 {
			continue
		}
		// Stop after page is full
		if len(rows) >= pageSize {
			break
		}

		rid := scanner.RID()
		row := scanner.Row()

		rows = append(rows, RowData{
			RID:    fmt.Sprintf("%d:%d", rid.PageID, rid.SlotID),
			Values: RowToJSON(table.Schema(), row),
		})
	}

	if err := scanner.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Scan error: %v", err))
		return
	}

	// Note: totalCount is approximate (only counts scanned rows, not actual total)
	// For exact count, would need full table scan
	resp := RowsResponse{
		Rows:       rows,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	}

	respondJSON(w, http.StatusOK, resp)
}

// handleInsertRow inserts a new row
func (s *Server) handleInsertRow(w http.ResponseWriter, r *http.Request, tableName string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	var req RowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Convert JSON to storage.Row
	row, err := JSONToRow(table.Schema(), req.Values)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid row data: %v", err))
		return
	}

	// Insert row
	rid, err := table.Insert(row)
	if err != nil {
		var uniqueViolation *storage.ErrUniqueViolation
		if errors.As(err, &uniqueViolation) {
			respondError(w, http.StatusConflict, fmt.Sprintf("Unique constraint violation: %v", err))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Insert failed: %v", err))
		return
	}

	respondJSON(w, http.StatusCreated, SuccessResponse{
		Success: true,
		Message: "Row inserted",
		RID:     fmt.Sprintf("%d:%d", rid.PageID, rid.SlotID),
	})
}

// handleGetRow fetches a single row by RID
func (s *Server) handleGetRow(w http.ResponseWriter, r *http.Request, tableName, ridStr string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	rid, err := parseRID(ridStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid RID: %v", err))
		return
	}

	row, found, err := table.Get(rid)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Get failed: %v", err))
		return
	}
	if !found {
		respondError(w, http.StatusNotFound, "Row not found")
		return
	}

	rowData := RowData{
		RID:    ridStr,
		Values: RowToJSON(table.Schema(), row),
	}

	respondJSON(w, http.StatusOK, rowData)
}

// handleUpdateRow updates an existing row
func (s *Server) handleUpdateRow(w http.ResponseWriter, r *http.Request, tableName, ridStr string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	rid, err := parseRID(ridStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid RID: %v", err))
		return
	}

	var req RowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Convert JSON to storage.Row
	newRow, err := JSONToRow(table.Schema(), req.Values)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid row data: %v", err))
		return
	}

	// Update row (delete + insert)
	newRID, found, err := table.Update(rid, newRow)
	if err != nil {
		var uniqueViolation *storage.ErrUniqueViolation
		if errors.As(err, &uniqueViolation) {
			respondError(w, http.StatusConflict, fmt.Sprintf("Unique constraint violation: %v", err))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Update failed: %v", err))
		return
	}
	if !found {
		respondError(w, http.StatusNotFound, "Row not found")
		return
	}

	respondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Row updated",
		RID:     fmt.Sprintf("%d:%d", newRID.PageID, newRID.SlotID),
	})
}

// handleDeleteRow deletes a row
func (s *Server) handleDeleteRow(w http.ResponseWriter, r *http.Request, tableName, ridStr string) {
	table, err := s.db.OpenTable(tableName)
	if err != nil {
		var notFound *storage.ErrTableNotFound
		if errors.As(err, &notFound) {
			respondError(w, http.StatusNotFound, fmt.Sprintf("Table %q not found", tableName))
			return
		}
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to open table: %v", err))
		return
	}

	rid, err := parseRID(ridStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid RID: %v", err))
		return
	}

	found, err := table.Delete(rid)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Delete failed: %v", err))
		return
	}
	if !found {
		respondError(w, http.StatusNotFound, "Row not found")
		return
	}

	respondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Row deleted",
	})
}

// parseRID parses "pageID:slotID" format
func parseRID(ridStr string) (storage.RID, error) {
	parts := strings.Split(ridStr, ":")
	if len(parts) != 2 {
		return storage.RID{}, fmt.Errorf("invalid RID format, expected pageID:slotID")
	}

	pageID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return storage.RID{}, fmt.Errorf("invalid pageID: %w", err)
	}

	slotID, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return storage.RID{}, fmt.Errorf("invalid slotID: %w", err)
	}

	return storage.RID{PageID: pageID, SlotID: storage.SlotID(slotID)}, nil
}

// respondJSON writes a JSON response
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Log error but can't write to response anymore
		fmt.Fprintf(io.Discard, "Failed to encode JSON: %v\n", err)
	}
}

// respondError writes a JSON error response
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, ErrorResponse{Error: message})
}

// handleQuery executes a SQL statement and returns the result set
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}
	if req.SQL == "" {
		respondError(w, http.StatusBadRequest, "sql is required")
		return
	}

	// Intercept USE [DATABASE] <name> before passing to the query engine.
	if nodes, parseErr := parser.ParseString(req.SQL); parseErr == nil && len(nodes) == 1 {
		if use, ok := nodes[0].(*parser.UseDatabaseStmt); ok {
			newDir := filepath.Join(s.parentDir, use.DBName)
			if err := s.switchDatabase(newDir); err != nil {
				respondError(w, http.StatusBadRequest, err.Error())
				return
			}
			respondJSON(w, http.StatusOK, QueryResponse{
				Columns:  []string{"message"},
				Rows:     [][]interface{}{{"Switched to database '" + use.DBName + "'"}},
				RowCount: 1,
			})
			return
		}
	}

	engine := query.NewEngine(s.db)
	start := time.Now()
	rs, err := engine.Execute(req.SQL)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var columns []string
	if rs.Schema != nil {
		for i := 0; i < rs.Schema.NumColumns(); i++ {
			columns = append(columns, rs.Schema.Column(i).Name)
		}
	}

	resultRows := make([][]interface{}, len(rs.Rows))
	for i, row := range rs.Rows {
		cells := make([]interface{}, len(row))
		for j, v := range row {
			cells[j] = valueToJSON(v)
		}
		resultRows[i] = cells
	}

	respondJSON(w, http.StatusOK, QueryResponse{
		Columns:         columns,
		Rows:            resultRows,
		RowCount:        len(rs.Rows),
		ExecutionTimeMs: elapsed,
	})
}
