package lexer

import (
	"strings"
)

// TokenType represents the type of a token
type TokenType string

const (
	TokenKeyword    TokenType = "KEYWORD"
	TokenIdentifier TokenType = "IDENTIFIER"
	TokenOperator   TokenType = "OPERATOR"
	TokenNumber     TokenType = "NUMBER"
	TokenString     TokenType = "STRING"
	TokenWhitespace TokenType = "WHITESPACE"
	TokenSeparator  TokenType = "SEPARATOR"
	TokenUnknown    TokenType = "UNKNOWN"
)

// Token represents a lexical token
type Token struct {
	Type  TokenType
	Value string
}

var keywords = map[string]bool{
	"select": true,
	"from":   true,
	"where":  true,
	"create": true,
	"table":  true,
	"insert": true,
	"into":   true,
	"values": true,
	"limit":  true,
	"and":    true,
	"or":     true,
	"null":   true,

	"update": true,
	"delete": true,
	"drop":   true,
}

var operators = map[string]bool{
	"=":  true,
	"!=": true,
	"<":  true,
	">":  true,
	"<=": true,
	">=": true,
}

var separators = map[rune]bool{
	',': true,
	';': true,
	'(': true,
	')': true,
	'*': true,
}

// Tokenize splits a string into a slice of tokens
func Tokenize(input string) []Token {
	var tokens []Token
	var current strings.Builder
	i := 0
	inputLength := len(input)

	for i < inputLength {
		ch := rune(input[i])

		// Handle whitespace
		if isWhitespace(ch) {
			if current.Len() > 0 {
				tokens = append(tokens, createToken(current.String()))
				current.Reset()
			}
			i++
			continue
		}

		// Handle separators
		if isSeparator(ch) {
			if current.Len() > 0 {
				tokens = append(tokens, createToken(current.String()))
				current.Reset()
			}
			tokens = append(tokens, Token{Type: TokenSeparator, Value: string(ch)})
			i++
			continue
		}

		// Handle operators
		if isOperator(string(ch)) || (inputLength > i+1 && isOperator(input[i:i+2])) {
			if current.Len() > 0 {
				tokens = append(tokens, createToken(current.String()))
				current.Reset()
				i++
			}
			if i+1 < inputLength && isOperator(input[i:i+2]) {
				tokens = append(tokens, Token{Type: TokenOperator, Value: input[i : i+2]})
				i += 2
			} else {
				tokens = append(tokens, Token{Type: TokenOperator, Value: string(ch)})
				i++
			}
			continue
		}

		// Handle strings
		if ch == '\'' || ch == '"' {
			if current.Len() > 0 {
				tokens = append(tokens, createToken(current.String()))
				current.Reset()
			}
			quote := ch
			i++
			for i < len(input) && rune(input[i]) != quote {
				current.WriteRune(rune(input[i]))
				i++
			}
			tokens = append(tokens, Token{Type: TokenString, Value: current.String()})
			current.Reset()
			if i < len(input) {
				i++ // skip closing quote
			}
			continue
		}

		current.WriteRune(ch)
		i++
	}

	// Add any remaining token
	if current.Len() > 0 {
		tokens = append(tokens, createToken(current.String()))
	}

	return tokens
}

// createToken determines the token type based on the value
func createToken(value string) Token {
	lower := strings.ToLower(value)

	if keywords[lower] {
		return Token{Type: TokenKeyword, Value: value}
	}

	if isNumeric(value) {
		return Token{Type: TokenNumber, Value: value}
	}

	return Token{Type: TokenIdentifier, Value: value}
}

func isWhitespace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func isOperator(s string) bool {
	return operators[s]
}

func isSeparator(ch rune) bool {
	return separators[ch]
}
func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			if ch != '.' {
				return false
			}
		}
	}
	return true
}

//CREATE TABLE table_name (column_name1 INT,column_name2 TEXT);
