package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/netmonitor/netmonitor/internal/db"
)

func main() {
	oldKey := flag.String("old", os.Getenv("OLD_ENCRYPTION_KEY"), "previous ENCRYPTION_KEY")
	newKey := flag.String("new", os.Getenv("NEW_ENCRYPTION_KEY"), "new ENCRYPTION_KEY")
	dsn := flag.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	flag.Parse()
	if *oldKey == "" || *newKey == "" || *dsn == "" {
		log.Fatal("OLD_ENCRYPTION_KEY, NEW_ENCRYPTION_KEY and DATABASE_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	n, err := db.RotateSecrets(ctx, pool, *oldKey, *newKey)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rotated %d encrypted records", n)
}
