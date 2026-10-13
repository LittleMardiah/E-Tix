package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	var (
		path     = flag.String("path", "./migrations", "path to migration files")
		database = flag.String("database", "", "database URL (required)")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-path <dir>] -database <url> <up|down|version|force <N>>\n", os.Args[0])
	}

	flag.Parse()

	args := flag.Args()
	if len(args) > 0 && args[0] == "help" {
		flag.Usage()
		os.Exit(0)
	}

	if *database == "" {
		fmt.Fprintf(os.Stderr, "error: -database is required\n")
		flag.Usage()
		os.Exit(1)
	}

	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "error: command required\n")
		flag.Usage()
		os.Exit(1)
	}

	m, err := migrate.New(fmt.Sprintf("file://%s", *path), *database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _, _ = m.Close() }()

	switch args[0] {
	case "up":
		if err := m.Up(); err != nil {
			if err == migrate.ErrNoChange {
				fmt.Println("no change")
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		v, _, _ := m.Version()
		fmt.Printf("migrated to version %d\n", v)
	case "down":
		if err := m.Down(); err != nil {
			if err == migrate.ErrNoChange {
				fmt.Println("no change")
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		v, _, _ := m.Version()
		fmt.Printf("migrated to version %d\n", v)
	case "version":
		v, dirty, err := m.Version()
		if err != nil {
			if err == migrate.ErrNoChange {
				fmt.Println("no change")
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("version %d dirty %t\n", v, dirty)
	case "force":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "error: force requires a version number\n")
			flag.Usage()
			os.Exit(1)
		}
		var ver int
		if _, err := fmt.Sscanf(args[1], "%d", &ver); err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid version: %v\n", err)
			os.Exit(1)
		}
		if err := m.Force(ver); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("forced to version %d\n", ver)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", args[0])
		flag.Usage()
		os.Exit(1)
	}
	os.Exit(0)
}
