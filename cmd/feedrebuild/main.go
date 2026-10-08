package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/joho/godotenv"

	"go-feed-system/internal/feed"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/database"
	"go-feed-system/pkg/redis"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "feedrebuild: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	flags := flag.NewFlagSet("feedrebuild", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var userIDs uint64ListFlag
	flags.Var(
		&userIDs,
		"user-id",
		"user ID to rebuild; repeat the flag for multiple users",
	)
	since := flags.Duration(
		"since",
		feed.DefaultRebuildWindow,
		"rebuild videos published within this duration",
	)
	batchSize := flags.Int(
		"batch-size",
		feed.DefaultRebuildBatchSize,
		"number of videos fetched per page",
	)
	execute := flags.Bool(
		"execute",
		false,
		"write rebuilt data to Redis; without this flag only a dry run is performed",
	)
	timeout := flags.Duration(
		"timeout",
		10*time.Minute,
		"maximum total execution time",
	)

	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return fmt.Errorf("-user-id must be provided at least once")
	}
	if *since <= 0 {
		return fmt.Errorf("-since must be positive")
	}
	if *timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := database.Open(ctx, database.MySQLConfig{
		DSN:             cfg.MySQL.DSN,
		MaxOpenConns:    cfg.MySQL.MaxOpenConns,
		MaxIdleConns:    cfg.MySQL.MaxIdleConns,
		ConnMaxLifetime: cfg.MySQL.ConnMaxLifetime,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	redisClient, err := redis.Open(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("open redis: %w", err)
	}
	defer func() {
		if err := redis.Close(redisClient); err != nil {
			logger.Error("close redis", "error", err)
		}
	}()

	repository := feed.NewGORMRepository(db)
	inbox := feed.NewRedisInbox(redisClient, feed.DefaultInboxMaxLen)
	rebuilder := feed.NewRebuilder(
		repository,
		inbox,
		cfg.Feed.FanoutFollowerThreshold,
		logger,
	)

	results, err := rebuilder.Rebuild(ctx, feed.RebuildOptions{
		UserIDs:   userIDs,
		Since:     time.Now().Add(-*since),
		BatchSize: *batchSize,
		Execute:   *execute,
	})
	if err != nil {
		return err
	}

	mode := "DRY RUN"
	if *execute {
		mode = "EXECUTED"
	}
	fmt.Fprintf(stdout, "%s\n", mode)

	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "USER_ID\tSCANNED\tELIGIBLE\tWRITTEN")
	for _, result := range results {
		fmt.Fprintf(
			writer,
			"%d\t%d\t%d\t%d\n",
			result.UserID,
			result.Scanned,
			result.Eligible,
			result.Written,
		)
	}
	return writer.Flush()
}

type uint64ListFlag []uint64

func (f *uint64ListFlag) String() string {
	values := make([]string, 0, len(*f))
	for _, value := range *f {
		values = append(values, strconv.FormatUint(value, 10))
	}
	return strings.Join(values, ",")
}

func (f *uint64ListFlag) Set(raw string) error {
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid user ID %q: %w", part, err)
		}
		if value == 0 {
			return errors.New("user ID must be positive")
		}
		*f = append(*f, value)
	}
	return nil
}
