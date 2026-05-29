package main

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"

	arcade "github.com/kingychiu/no-js-todolist"
)

func main() {
	sqldb, err := sql.Open("sqlite3", "file:arcade.db?_journal=WAL&_busy_timeout=5000&_sync=NORMAL&_fk=on")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() { _ = sqldb.Close() }()

	e, err := arcade.NewApp(sqldb)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}

	log.Println("listening on :8080")
	if err := e.Start(":8080"); err != nil {
		log.Fatalf("server: %v", err)
	}
}
