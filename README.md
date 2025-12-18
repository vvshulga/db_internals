# db_internals

Education project for DB Internals CS osvita course. This project implements a SQL lexer and parser that tokenizes and builds an Abstract Syntax Tree (AST) for SQL queries.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Using the Lexer](#using-the-lexer)
- [Using the Parser](#using-the-parser)
- [Using the CLI Tool](#using-the-cli-tool)
- [Supported SQL](#supported-sql)
- [Testing](#testing)

## Prerequisites

### macOS

To run this application on macOS, you need:

- **Go 1.25 or later**: [Download Go](https://golang.org/dl/)
  - Install using Homebrew (recommended):
    ```bash
    brew install go
    ```
  - Verify installation:
    ```bash
    go version
    ```

- **Git**: [Download Git](https://git-scm.com/) or install via Homebrew:
  ```bash
  brew install git
  ```

### Other Requirements

- A terminal/shell environment (zsh, bash, etc.)
- Code editor (VS Code, GoLand, etc.) - optional

## Installation

### Clone the Repository

```bash
git clone https://github.com/vvshulga/db_internals.git
cd db_internals
```

### Download Dependencies

```bash
go mod download
```

## Quick Start

### Build the Application

```bash
go build -o db_internals .
```

### Run the CLI Tool

```bash
./db_internals "SELECT * FROM users WHERE id = 1"
```

## Using the Lexer

The lexer tokenizes SQL input into structured tokens. Each token has a type and value.

### Lexer Package

Location: `lexer/lexer.go`

### Token Types

- `KEYWORD`: SQL keywords (SELECT, FROM, WHERE, INSERT, CREATE, etc.)
- `IDENTIFIER`: Table/column names
- `OPERATOR`: Comparison operators (=, !=, <, >, <=, >=)
- `NUMBER`: Numeric literals
- `STRING`: String literals (single or double quoted)
- `SEPARATOR`: Punctuation (parentheses, commas, asterisk, semicolon)

### Basic Usage

```go
package main

import (
    "fmt"
    "github.com/vvshulga/db_internals/lexer"
)

func main() {
    query := "SELECT id, name FROM users WHERE age > 18"
    tokens := lexer.Tokenize(query)
    
    for _, token := range tokens {
        fmt.Printf("Type: %s, Value: %s\n", token.Type, token.Value)
    }
}
```

### Token Output Example

```
Type: KEYWORD, Value: SELECT
Type: IDENTIFIER, Value: id
Type: SEPARATOR, Value: ,
Type: IDENTIFIER, Value: name
Type: KEYWORD, Value: FROM
Type: IDENTIFIER, Value: users
Type: KEYWORD, Value: WHERE
Type: IDENTIFIER, Value: age
Type: OPERATOR, Value: >
Type: NUMBER, Value: 18
```

## Using the Parser

The parser builds an Abstract Syntax Tree (AST) from tokens, validating SQL syntax and structure.

### Parser Package

Location: `parser/parser.go`

### AST Node Types

#### Statement Nodes

- `SelectStmt`: SELECT queries with optional WHERE and LIMIT clauses
- `InsertStmt`: INSERT queries with column values
- `CreateTableStmt`: CREATE TABLE queries with column definitions

#### Expression Nodes

- `ColumnRef`: Column references (e.g., `id`, `users.name`)
- `LiteralInt`: Integer literals (e.g., `42`, `1000`)
- `LiteralString`: String literals (e.g., `'Alice'`, `"Bob"`)
- `ComparisonOp`: Comparison expressions (e.g., `id > 18`)
- `LogicalOp`: AND/OR operations
- `BinaryOp`: Binary expressions

### Basic Usage

```go
package main

import (
    "fmt"
    "github.com/vvshulga/db_internals/parser"
)

func main() {
    query := "SELECT id, name FROM users WHERE id = 5"
    
    nodes, err := parser.ParseString(query)
    if err != nil {
        fmt.Println("Parse error:", err)
        return
    }
    
    // Use the AST nodes
    for _, node := range nodes {
        fmt.Printf("%+v\n", node)
    }
}
```

### Printing the AST

```go
astStr := parser.PrintAST(nodes)
fmt.Println(astStr)
```

### Example AST Output

```
SELECT
  Projections: [id, name]
  From: users
  Where: (id = 5)
```

## Using the CLI Tool

The CLI tool provides an interactive way to tokenize and parse SQL queries.

### Command Format

```bash
./db_internals "<SQL_QUERY>"
```

### Examples

#### Example 1: Simple SELECT Query

```bash
./db_internals "SELECT * FROM users"
```

Output:
```
Received query: SELECT * FROM users

Tokens:
  Type: KEYWORD, Value: SELECT
  Type: SEPARATOR, Value: *
  Type: KEYWORD, Value: FROM
  Type: IDENTIFIER, Value: users

AST:
SELECT
  Projections: [*]
  From: users
```

#### Example 2: SELECT with WHERE Clause

```bash
./db_internals "SELECT id, name FROM users WHERE age > 18 LIMIT 10"
```

#### Example 3: INSERT Query

```bash
./db_internals "INSERT INTO products VALUES (1, 'Laptop', 999)"
```

#### Example 4: CREATE TABLE Query

```bash
./db_internals "CREATE TABLE employees (id INT, name TEXT, salary INT)"
```

### Error Handling

The tool validates SQL syntax and reports errors:

```bash
./db_internals "SELECT FROM users"
```

Output:
```
Received query: SELECT FROM users

Tokens:
  Type: KEYWORD, Value: SELECT
  Type: KEYWORD, Value: FROM
  Type: IDENTIFIER, Value: users

Parse error: expected projections or * after SELECT
```

## Supported SQL

### SELECT Statements

```sql
SELECT col1, col2 FROM table_name;
SELECT * FROM table_name;
SELECT col1 FROM table_name WHERE col1 > 10 AND col2 = 'value';
SELECT col1 FROM table_name WHERE col1 = 5 LIMIT 10;
```

### INSERT Statements

```sql
INSERT INTO table_name VALUES (1, 'Alice', 42);
INSERT INTO table_name (col1, col2) VALUES (100, 'Bob');
```

### CREATE TABLE Statements

```sql
CREATE TABLE users (id INT, name TEXT);
CREATE TABLE products (id INT, name TEXT, price INT);
```

### WHERE Clauses

Supported operators:
- `=`: Equal
- `!=`: Not equal
- `<`: Less than
- `>`: Greater than
- `<=`: Less than or equal
- `>=`: Greater than or equal

Supported logical operators:
- `AND`: Logical AND
- `OR`: Logical OR

## Testing

The project includes comprehensive test suites for both lexer and parser.

### Run All Tests

```bash
go test ./...
```

### Run Tests with Verbose Output

```bash
go test -v ./...
```

### Run Specific Package Tests

```bash
go test -v ./lexer
go test -v ./parser
```

### Run Tests with Coverage Report

```bash
go test -v -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Test Categories

#### Lexer Tests (`lexer/lexer_test.go`)

- Tokenization of SQL statements
- Handling of strings, numbers, operators
- Unclosed string handling
- Mismatched parentheses detection

#### Parser Tests (`parser/parser_test.go`)

- AST node creation and validation
- AST structure correctness
- Error handling for malformed queries
- Unclosed strings and mismatched parentheses

### Current Test Results

All tests pass with comprehensive coverage of:
- 12+ lexer test cases
- 9+ parser test cases including edge cases

## Architecture

### Project Structure

```
db_internals/
├── main.go           # CLI entry point
├── go.mod            # Go module definition
├── README.md         # Documentation
├── grammar.bnf       # SQL grammar specification
├── lexer/            # Tokenization package
│   ├── lexer.go      # Lexer implementation
│   └── lexer_test.go # Lexer tests
├── parser/           # Parser package
│   ├── parser.go     # Parser and AST definitions
│   └── parser_test.go # Parser tests
└── .github/
    └── workflows/
        └── tests.yml # GitHub Actions workflow
```

### Data Flow

```
SQL Query (string)
    ↓
Lexer (lexer.Tokenize)
    ↓
Token Stream
    ↓
Parser (parser.ParseString)
    ↓
AST (Abstract Syntax Tree)
    ↓
Printer (parser.PrintAST)
    ↓
Formatted Output
```

## Continuous Integration

This project uses GitHub Actions to automatically run tests on every push. See `.github/workflows/tests.yml` for the workflow configuration.

## License

Educational project for DB Internals course.
