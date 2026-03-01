package main

// InfoResponse returns database metadata
type InfoResponse struct {
	DatabaseDir string   `json:"database_dir"`
	CurrentDB   string   `json:"current_db"` // base name of the current database directory
	TableCount  int      `json:"table_count"`
	TableNames  []string `json:"table_names"`
}

// TableSchema represents the structure of a table
type TableSchema struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

// Column represents a single column in a table schema
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`      // "INT", "VARCHAR(64)", etc.
	Nullable bool   `json:"nullable"`
}

// RowsResponse returns paginated row data
type RowsResponse struct {
	Rows       []RowData `json:"rows"`
	TotalCount int       `json:"total_count"`
	Page       int       `json:"page"`
	PageSize   int       `json:"page_size"`
}

// RowData represents a single row with its RID and values
type RowData struct {
	RID    string                 `json:"rid"`    // "pageID:slotID"
	Values map[string]interface{} `json:"values"` // column → value
}

// CreateTableRequest is the payload for creating a table
type CreateTableRequest struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

// RowRequest is the payload for inserting or updating a row
type RowRequest struct {
	Values map[string]interface{} `json:"values"` // column → value
}

// SuccessResponse is a generic success response
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	RID     string `json:"rid,omitempty"` // for insert operations
}

// ErrorResponse is a generic error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// SwitchDBRequest is the payload for POST /api/db/switch
type SwitchDBRequest struct {
	Name string `json:"name"`
}

// QueryRequest is the payload for executing a SQL statement
type QueryRequest struct {
	SQL string `json:"sql"`
}

// QueryResponse returns the results of a SQL statement
type QueryResponse struct {
	Columns         []string        `json:"columns"`
	Rows            [][]interface{} `json:"rows"`
	RowCount        int             `json:"row_count"`
	ExecutionTimeMs int64           `json:"execution_time_ms"`
}
