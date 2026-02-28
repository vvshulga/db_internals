# DB Internals Web Admin

React-based web administration interface for the db_internals database engine.

## Features

- **Database Overview**: View database directory, table count, and table list
- **Table Management**: Create and drop tables with custom schemas
- **Table Search**: Search tables by name with instant filtering
- **Schema Inspection**: View table schemas with column types and nullable flags
- **Record CRUD**: Insert, view, update, and delete records
- **Pagination**: Navigate through large result sets with configurable page sizes (10, 25, 50, 100 rows per page)
- **Column Search**: Search rows by column value with real-time filtering
- **Type Support**: All data types (INT, BIGINT, FLOAT, DOUBLE, BOOLEAN, VARCHAR, TEXT, DATETIME)
- **NULL Handling**: Support for nullable columns

## Architecture

- **Backend**: Go HTTP REST API server using the storage package directly
- **Frontend**: React 18 + TypeScript with Vite for fast development
- **API**: RESTful JSON over HTTP on port 8080
- **Dev Proxy**: Vite dev server (port 5173) proxies API requests to backend

## Prerequisites

- Go 1.21 or later
- Node.js 18 or later
- npm or yarn

## Quick Start

### Development Mode

1. **Setup frontend dependencies:**
   ```bash
   cd frontend
   npm install
   cd ..
   ```

2. **Start backend server** (in one terminal):
   ```bash
   make dev-backend
   ```
   Backend starts on http://localhost:8080 with database at `./testdata`

3. **Start frontend dev server** (in another terminal):
   ```bash
   make dev-frontend
   ```
   Frontend starts on http://localhost:5173

4. **Open browser:**
   Navigate to http://localhost:5173

### Production Build

Build both backend and frontend:
```bash
make all
```

Run the compiled server:
```bash
DB_DIR=/path/to/database ./ui_admin_server
```

The server serves the static frontend at http://localhost:8080

## Seeding the Database

A sample `seed.sql` file is provided with test data for an e-commerce system. It creates 5 related tables (users, categories, products, orders, order_items) with ~75 rows of realistic data.

### Using the seeddb Tool

The `seeddb` tool is a SQL execution utility that parses SQL files and executes CREATE TABLE and INSERT statements against the storage layer. This is the **first integration** between the SQL parser and storage in the codebase.

**Build the tool:**
```bash
go build -o seeddb ./cmd/seeddb
```

**Run against a database:**
```bash
# Seed a fresh database
./seeddb -db ./testdata -sql ui_admin/seed.sql

# Or use custom paths
./seeddb -db /path/to/database -sql /path/to/seed.sql
```

**View the results:**
```bash
# Start the web admin
make dev-backend   # Terminal 1
make dev-frontend  # Terminal 2

# Navigate to http://localhost:5173/tables
```

**Limitations:**
- Only CREATE TABLE and INSERT statements are supported
- No transaction support (each statement is auto-committed)

## API Endpoints

### Database

- `GET /api/info` - Get database metadata (directory, table count, table names)

### Tables

- `GET /api/tables` - List all tables
- `POST /api/tables` - Create table
  ```json
  {
    "name": "users",
    "columns": [
      {"name": "id", "type": "INT", "nullable": false},
      {"name": "name", "type": "VARCHAR(255)", "nullable": false}
    ]
  }
  ```
- `GET /api/tables/:name` - Get table schema
- `DELETE /api/tables/:name` - Drop table

### Rows

- `GET /api/tables/:name/rows?page=1&page_size=50` - Scan rows (paginated)
- `POST /api/tables/:name/rows` - Insert row
  ```json
  {
    "values": {"id": 1, "name": "Alice"}
  }
  ```
- `GET /api/tables/:name/rows/:rid` - Get single row
- `PUT /api/tables/:name/rows/:rid` - Update row
- `DELETE /api/tables/:name/rows/:rid` - Delete row

## Data Types

Supported column types:
- `INT` - 32-bit integer
- `BIGINT` - 64-bit integer
- `FLOAT` - 32-bit floating point
- `DOUBLE` - 64-bit floating point
- `BOOLEAN` - true/false
- `VARCHAR(N)` - Variable-length string (max N bytes)
- `TEXT` - Variable-length text
- `DATETIME` - Unix timestamp (RFC3339 format in JSON)

## Environment Variables

### Backend

- `DB_DIR` - Database directory path (default: `./data`)
- `PORT` - HTTP server port (default: `8080`)

### Frontend

- `VITE_API_BASE` - API base URL (default: `http://localhost:8080/api`)

## Development Commands

```bash
# Setup
make setup              # Install frontend dependencies

# Development
make dev-backend        # Start backend dev server
make dev-frontend       # Start frontend dev server

# Building
make backend           # Build backend only
make frontend          # Build frontend only
make all               # Build both

# Testing
make test-backend      # Run backend tests with race detector

# Cleanup
make clean             # Remove build artifacts
```

## Project Structure

```
ui_admin/
├── backend/
│   ├── main.go              # HTTP server entry point
│   ├── handlers.go          # REST endpoint handlers
│   ├── types.go             # JSON request/response types
│   └── serialization.go     # storage ↔ JSON conversion
├── frontend/
│   ├── src/
│   │   ├── main.tsx         # React entry point
│   │   ├── App.tsx          # Root component with routing
│   │   ├── api/
│   │   │   └── client.ts    # API client
│   │   ├── components/
│   │   │   ├── DatabaseInfo.tsx   # Database overview
│   │   │   ├── TableList.tsx      # Table list
│   │   │   ├── TableView.tsx      # Table viewer
│   │   │   ├── TableCreate.tsx    # Create table form
│   │   │   ├── RowEditor.tsx      # Insert/update row form
│   │   │   └── SchemaView.tsx     # Schema display
│   │   └── styles/
│   │       └── app.css      # Application styles
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   └── index.html
├── Makefile
└── README.md
```

## Known Limitations

- **Single database**: Only one database per backend instance
- **No table rename**: Storage layer doesn't provide rename API
- **Client-side filtering**: Search filters rows already loaded (max 1000 rows)
- **No authentication**: Local development tool only
- **No real-time updates**: Manual refresh required
- **No index management**: Indexes not exposed in UI

## Future Enhancements

### Short-term
- Server-side search (for tables with >1000 rows)
- Table statistics (row count, disk size)
- Index management UI
- SQL query execution tab
- Advanced filtering (multiple columns, operators)

### Medium-term
- Multi-database support
- Search/filter rows
- Export/import CSV
- Table rename (requires storage layer support)

### Long-term
- Authentication and user management
- Real-time updates with WebSockets
- Visual query builder
- Performance monitoring

## Troubleshooting

### Backend won't start

**Error:** `Failed to open database: ...`

**Solution:** Check that `DB_DIR` points to a valid directory with write permissions.

### Frontend can't connect to backend

**Error:** Network errors in browser console

**Solution:** Ensure backend is running on port 8080. Check CORS settings.

### Build errors in frontend

**Error:** `Cannot find module 'react'`

**Solution:** Run `make setup` or `cd frontend && npm install`

### Type errors in TypeScript

**Error:** `Property 'X' does not exist on type 'Y'`

**Solution:** Check that API types in `client.ts` match backend JSON responses.

## License

Part of the db_internals educational database project.
