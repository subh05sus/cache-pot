// Command cache-pot is the Cache-Pot server and its MCP bridge.
//
// Usage:
//
//	cache-pot [flags]          start the RESP2 server (default)
//	cache-pot mcp [flags]      run the MCP stdio bridge against a running server
//
// Every flag has a CACHEPOT_* environment-variable equivalent so the binary is
// easy to configure in containers.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/subh05sus/cache-pot/internal/dashboard"
	"github.com/subh05sus/cache-pot/internal/embed"
	"github.com/subh05sus/cache-pot/internal/mcp"
	"github.com/subh05sus/cache-pot/internal/persist"
	"github.com/subh05sus/cache-pot/internal/server"
	"github.com/subh05sus/cache-pot/internal/store"
)

func main() {
	// Subcommand dispatch: "cache-pot mcp ..." runs the MCP bridge.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := runMCP(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "cache-pot mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := runServer(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cache-pot: %v\n", err)
		os.Exit(1)
	}
}

func runServer(argv []string) error {
	fs := flag.NewFlagSet("cache-pot", flag.ExitOnError)
	addr := fs.String("addr", env("CACHEPOT_ADDR", ":6379"), "TCP listen address (host:port)")
	password := fs.String("auth", env("CACHEPOT_AUTH", ""), "require this AUTH password ('' disables auth)")
	snapPath := fs.String("snapshot-path", env("CACHEPOT_SNAPSHOT_PATH", "cache-pot.snapshot"), "snapshot file path ('' disables persistence)")
	snapInterval := fs.Duration("snapshot-interval", envDuration("CACHEPOT_SNAPSHOT_INTERVAL", 60*time.Second), "how often to snapshot to disk")
	sweepInterval := fs.Duration("sweep-interval", envDuration("CACHEPOT_SWEEP_INTERVAL", 10*time.Second), "how often the expiry sweeper runs")
	dashAddr := fs.String("dashboard-addr", env("CACHEPOT_DASHBOARD_ADDR", ":8080"), "web dashboard address ('' disables it)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Parse(argv)

	if *showVersion {
		fmt.Println("cache-pot", server.Version)
		return nil
	}

	st := store.New()

	// Snapshot persistence (optional).
	var snap *persist.Snapshotter
	if *snapPath != "" {
		snap = persist.New(st, *snapPath)
		if loaded, err := snap.Load(); err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		} else if loaded {
			fmt.Printf("cache-pot: loaded snapshot from %s (%d keys)\n", *snapPath, st.DBSize())
		}
	}

	// Optional embeddings provider for the semantic cache.
	emb := embed.New(embed.Config{
		URL:    env("CACHEPOT_EMBED_URL", ""),
		Model:  env("CACHEPOT_EMBED_MODEL", "text-embedding-3-small"),
		APIKey: env("CACHEPOT_EMBED_KEY", ""),
	})
	if emb.Configured() {
		fmt.Println("cache-pot: semantic cache embeddings enabled via CACHEPOT_EMBED_URL")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st.StartSweeper(ctx, *sweepInterval)

	srv := server.New(st, server.Config{
		Addr:        *addr,
		Password:    *password,
		Snapshotter: snap,
		Embed:       emb,
	})

	// Background snapshotting until shutdown, then a final save.
	snapStop := make(chan struct{})
	snapDone := make(chan struct{})
	if snap != nil {
		go func() {
			snap.StartAuto(*snapInterval, snapStop)
			close(snapDone)
		}()
	} else {
		close(snapDone)
	}

	// Optional dashboard.
	if *dashAddr != "" {
		dash := dashboard.New(srv, *dashAddr)
		go func() {
			fmt.Printf("cache-pot: dashboard on http://localhost%s\n", *dashAddr)
			if err := dash.ListenAndServe(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "cache-pot: dashboard error: %v\n", err)
			}
		}()
	}

	err := srv.ListenAndServe(ctx)

	// Shut down persistence and take a final snapshot.
	close(snapStop)
	<-snapDone
	if snap != nil {
		if serr := snap.Save(); serr != nil {
			fmt.Fprintf(os.Stderr, "cache-pot: final snapshot failed: %v\n", serr)
		} else {
			fmt.Println("cache-pot: final snapshot written")
		}
	}
	return err
}

func runMCP(argv []string) error {
	fs := flag.NewFlagSet("cache-pot mcp", flag.ExitOnError)
	addr := fs.String("addr", env("CACHEPOT_MCP_ADDR", "localhost:6379"), "address of the running Cache-Pot server")
	fs.Parse(argv)
	return mcp.Serve(*addr, os.Stdin, os.Stdout)
}

// env returns the value of key or def if unset/empty.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	// Allow a bare integer to mean seconds.
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
