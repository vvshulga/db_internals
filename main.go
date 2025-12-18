package main

import (
	"fmt"
	"os"

	"github.com/vvshulga/db_internals/lexer"
	"github.com/vvshulga/db_internals/parser"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: db_internals <query>")
		os.Exit(1)
	}

	// Read all command line arguments after the program name
	text := os.Args[1]
	fmt.Println("Received query:", text)

	// Tokenize the input
	tokens := lexer.Tokenize(text)
	fmt.Println("\nTokens:")
	for _, token := range tokens {
		fmt.Printf("  Type: %s, Value: %s\n", token.Type, token.Value)
	}

	// Parse and print AST
	nodes, err := parser.ParseString(text)
	if err != nil {
		fmt.Println("Parse error:", err)
		os.Exit(1)
	}
	fmt.Println("\nAST:")
	fmt.Print(parser.PrintAST(nodes))
}
