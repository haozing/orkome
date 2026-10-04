package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"orkome/internal/platform/pgmigrate"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "database DSN (defaults to DATABASE_URL)")
	dir := flag.String("dir", "migrations", "migrations directory")
	flag.Parse()

	if *dsn == "" {
		fatal("-dsn or DATABASE_URL required")
	}
	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "up"
	}
	limit := 0
	if arg := flag.Arg(1); arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 0 {
			fatal("the second argument must be a non-negative integer (number of steps to apply/roll back)")
		}
		limit = n
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fatal("connect database: " + err.Error())
	}
	defer pool.Close()

	m := pgmigrate.New(pool, *dir)
	switch cmd {
	case "up":
		applied, err := m.Up(ctx, limit)
		if err != nil {
			fatal("up: " + err.Error())
		}
		if len(applied) == 0 {
			fmt.Println("up: no pending migrations (idempotent)")
			return
		}
		for _, v := range applied {
			fmt.Println("applied", v)
		}
	case "down":
		rolled, err := m.Down(ctx, limit)
		if err != nil {
			fatal("down: " + err.Error())
		}
		if len(rolled) == 0 {
			fmt.Println("down: no migrations to roll back")
			return
		}
		for _, v := range rolled {
			fmt.Println("rolled back", v)
		}
	case "status":
		rows, err := m.Status(ctx)
		if err != nil {
			fatal("status: " + err.Error())
		}
		for _, r := range rows {
			state := "pending"
			if r.Applied {
				state = "applied at " + *r.AppliedAt
			}
			fmt.Printf("%s  %s\n", r.Version, state)
		}
	default:
		fatal("unknown subcommand " + cmd + "（up|down|status）")
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "migrate: "+msg)
	os.Exit(1)
}
