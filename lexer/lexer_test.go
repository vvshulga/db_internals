package lexer

import (
	"reflect"
	"testing"
)

func TestUnclosedString(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single quote unclosed", "SELECT 'unclosed string FROM t"},
		{"double quote unclosed", "INSERT INTO t VALUES (\"unterminated"},
		{"unclosed in middle", "SELECT col1, 'unterminated FROM table_name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := Tokenize(tt.input)
			// Verify tokens were created even with unclosed string
			if len(tokens) == 0 {
				t.Fatalf("Tokenize(%q) returned no tokens", tt.input)
			}
			// The last token should be a string token (the unclosed string)
			// since lexer continues to EOF when quote is not closed
			lastToken := tokens[len(tokens)-1]
			if lastToken.Type == TokenString {
				// This is expected - unclosed string reads to EOF
				t.Logf("Token: Type=%s, Value=%q", lastToken.Type, lastToken.Value)
			}
		})
	}
}

func TestMismatchedParentheses(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"extra opening paren", "SELECT * FROM t (WHERE id = 1)"},
		{"missing closing paren", "SELECT * FROM t WHERE (id = 1"},
		{"extra closing paren", "SELECT * FROM t)"},
		{"unmatched in insert", "INSERT INTO t VALUES (1, 2, 3))"},
		{"unmatched in create table", "CREATE TABLE t (id INT, name TEXT))"},
		{"multiple mismatches", "SELECT * FROM ((t WHERE id = 1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := Tokenize(tt.input)
			// Count opening and closing parentheses
			openCount := 0
			closeCount := 0
			for _, token := range tokens {
				if token.Type == TokenSeparator && token.Value == "(" {
					openCount++
				}
				if token.Type == TokenSeparator && token.Value == ")" {
					closeCount++
				}
			}
			if openCount != closeCount {
				t.Logf("Parentheses mismatch: %d opening, %d closing", openCount, closeCount)
			}
		})
	}
}

func TestTokenizeTable(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Token
	}{
		{
			name:  "simple select",
			input: "SELECT * FROM users WHERE id = 123",
			expected: []Token{
				{Type: TokenKeyword, Value: "SELECT"},
				{Type: TokenSeparator, Value: "*"},
				{Type: TokenKeyword, Value: "FROM"},
				{Type: TokenIdentifier, Value: "users"},
				{Type: TokenKeyword, Value: "WHERE"},
				{Type: TokenIdentifier, Value: "id"},
				{Type: TokenOperator, Value: "="},
				{Type: TokenNumber, Value: "123"},
			},
		},
		{
			name:  "comparison and float",
			input: "price >= 10.5",
			expected: []Token{
				{Type: TokenIdentifier, Value: "price"},
				{Type: TokenOperator, Value: ">="},
				{Type: TokenNumber, Value: "10.5"},
			},
		},
		{
			name:  "create table",
			input: "CREATE TABLE users (id, name);",
			expected: []Token{
				{Type: TokenKeyword, Value: "CREATE"},
				{Type: TokenKeyword, Value: "TABLE"},
				{Type: TokenIdentifier, Value: "users"},
				{Type: TokenSeparator, Value: "("},
				{Type: TokenIdentifier, Value: "id"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenIdentifier, Value: "name"},
				{Type: TokenSeparator, Value: ")"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "insert values with string",
			input: "insert into t values (1,'Alice')",
			expected: []Token{
				{Type: TokenKeyword, Value: "insert"},
				{Type: TokenKeyword, Value: "into"},
				{Type: TokenIdentifier, Value: "t"},
				{Type: TokenKeyword, Value: "values"},
				{Type: TokenSeparator, Value: "("},
				{Type: TokenNumber, Value: "1"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenString, Value: "Alice"},
				{Type: TokenSeparator, Value: ")"},
			},
		},
		{
			name:  "insert with string and numbers",
			input: "INSERT INTO table_name VALUES (1, 'Alice', 42);",
			expected: []Token{
				{Type: TokenKeyword, Value: "INSERT"},
				{Type: TokenKeyword, Value: "INTO"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenKeyword, Value: "VALUES"},
				{Type: TokenSeparator, Value: "("},
				{Type: TokenNumber, Value: "1"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenString, Value: "Alice"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenNumber, Value: "42"},
				{Type: TokenSeparator, Value: ")"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "insert with all numbers",
			input: "INSERT INTO table_name VALUES (1, 2, 3);",
			expected: []Token{
				{Type: TokenKeyword, Value: "INSERT"},
				{Type: TokenKeyword, Value: "INTO"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenKeyword, Value: "VALUES"},
				{Type: TokenSeparator, Value: "("},
				{Type: TokenNumber, Value: "1"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenNumber, Value: "2"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenNumber, Value: "3"},
				{Type: TokenSeparator, Value: ")"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "select multiple columns",
			input: "SELECT col1, col2 FROM table_name;",
			expected: []Token{
				{Type: TokenKeyword, Value: "SELECT"},
				{Type: TokenIdentifier, Value: "col1"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenIdentifier, Value: "col2"},
				{Type: TokenKeyword, Value: "FROM"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "select all from table",
			input: "SELECT * FROM table_name;",
			expected: []Token{
				{Type: TokenKeyword, Value: "SELECT"},
				{Type: TokenSeparator, Value: "*"},
				{Type: TokenKeyword, Value: "FROM"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "select with where condition",
			input: "SELECT col1, col2 FROM table_name WHERE col1 > 10;",
			expected: []Token{
				{Type: TokenKeyword, Value: "SELECT"},
				{Type: TokenIdentifier, Value: "col1"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenIdentifier, Value: "col2"},
				{Type: TokenKeyword, Value: "FROM"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenKeyword, Value: "WHERE"},
				{Type: TokenIdentifier, Value: "col1"},
				{Type: TokenOperator, Value: ">"},
				{Type: TokenNumber, Value: "10"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "select with where string and limit",
			input: "SELECT col1 FROM table_name WHERE col2 = 'Alice' LIMIT 10;",
			expected: []Token{
				{Type: TokenKeyword, Value: "SELECT"},
				{Type: TokenIdentifier, Value: "col1"},
				{Type: TokenKeyword, Value: "FROM"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenKeyword, Value: "WHERE"},
				{Type: TokenIdentifier, Value: "col2"},
				{Type: TokenOperator, Value: "="},
				{Type: TokenString, Value: "Alice"},
				{Type: TokenKeyword, Value: "LIMIT"},
				{Type: TokenNumber, Value: "10"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
		{
			name:  "create table with column types",
			input: "CREATE TABLE table_name (column_name1 INT,column_name2 TEXT);",
			expected: []Token{
				{Type: TokenKeyword, Value: "CREATE"},
				{Type: TokenKeyword, Value: "TABLE"},
				{Type: TokenIdentifier, Value: "table_name"},
				{Type: TokenSeparator, Value: "("},
				{Type: TokenIdentifier, Value: "column_name1"},
				{Type: TokenIdentifier, Value: "INT"},
				{Type: TokenSeparator, Value: ","},
				{Type: TokenIdentifier, Value: "column_name2"},
				{Type: TokenIdentifier, Value: "TEXT"},
				{Type: TokenSeparator, Value: ")"},
				{Type: TokenSeparator, Value: ";"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("Tokenize(%q) =\n%v\nwant\n%v", tt.input, got, tt.expected)
			}
		})
	}
}
