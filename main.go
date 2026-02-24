package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/query"
	"github.com/vvshulga/db_internals/storage"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: db_internals <database_dir> <sql_query>")
		fmt.Println("Example: db_internals /tmp/mydb \"SELECT * FROM users\"")
		os.Exit(1)
	}

	dbDir := os.Args[1]
	sql := os.Args[2]

	// Open database
	db, err := storage.OpenDB(dbDir)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Parse SQL
	nodes, err := parser.ParseString(sql)
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		os.Exit(1)
	}
	if len(nodes) == 0 {
		fmt.Println("No statements to execute")
		os.Exit(0)
	}

	// Plan
	planner := query.NewPlanner(db)
	logical, err := planner.Plan(nodes[0])
	if err != nil {
		fmt.Printf("Planning error: %v\n", err)
		os.Exit(1)
	}

	// Optimize
	optimizer := query.NewOptimizer(db)
	physical, err := optimizer.Optimize(logical)
	if err != nil {
		fmt.Printf("Optimization error: %v\n", err)
		os.Exit(1)
	}

	// Execute
	if err := physical.Open(); err != nil {
		fmt.Printf("Execution error: %v\n", err)
		os.Exit(1)
	}
	defer physical.Close()

	// Print results
	schema := physical.Schema()
	if schema != nil {
		// Print column headers
		for i := 0; i < schema.NumColumns(); i++ {
			if i > 0 {
				fmt.Print(" | ")
			}
			fmt.Print(schema.Column(i).Name)
		}
		fmt.Println()
		fmt.Println("---")
	}

	// Print rows
	rowCount := 0
	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Execution error: %v\n", err)
			os.Exit(1)
		}

		// Print row values
		for i, val := range row {
			if i > 0 {
				fmt.Print(" | ")
			}
			if val.IsNull() {
				fmt.Print("NULL")
			} else {
				fmt.Print(val.String())
			}
		}
		fmt.Println()
		rowCount++
	}

	fmt.Printf("\n%d row(s) returned\n", rowCount)
}
