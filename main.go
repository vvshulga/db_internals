package main

import (
	"fmt"
	"os"

	"github.com/vvshulga/db_internals/query"
	"github.com/vvshulga/db_internals/storage"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: db_internals <database_dir> <sql_query>")
		fmt.Println("Example: db_internals /tmp/mydb \"SELECT * FROM users\"")
		os.Exit(1)
	}

	db, err := storage.OpenDB(os.Args[1])
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	rs, err := query.NewEngine(db).Execute(os.Args[2])
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	rs.Print(os.Stdout)
}
