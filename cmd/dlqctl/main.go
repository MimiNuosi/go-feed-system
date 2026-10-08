package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/joho/godotenv"

	"go-feed-system/pkg/config"
)

const (
	defaultListLimit       = 20
	defaultReplayScanLimit = 1000
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "dlqctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("subcommand is required")
	}

	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	switch args[0] {
	case "list":
		return runList(ctx, cfg, args[1:], stdout, stderr, logger)
	case "replay":
		return runReplay(ctx, cfg, args[1:], stdout, stderr, logger)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runList(
	ctx context.Context,
	cfg config.Config,
	args []string,
	stdout, stderr io.Writer,
	logger *slog.Logger,
) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", defaultListLimit, "maximum number of messages to inspect")
	if err := flags.Parse(args); err != nil {
		return err
	}

	session, err := openDLQSession(ctx, cfg.RabbitMQ, logger)
	if err != nil {
		return err
	}
	summaries, listErr := listDeadLetters(ctx, cfg.RabbitMQ, session.channel, *limit)
	closeErr := session.Close()
	if listErr != nil {
		return listErr
	}
	if closeErr != nil {
		return closeErr
	}

	if len(summaries) == 0 {
		fmt.Fprintln(stdout, "no dead letters found")
		return nil
	}

	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "INDEX\tMESSAGE_ID\tTYPE\tRETRY_COUNT\tBODY_SIZE\tTIMESTAMP")
	for i, summary := range summaries {
		fmt.Fprintf(
			writer,
			"%d\t%s\t%s\t%d\t%d\t%s\n",
			i+1,
			summary.MessageID,
			summary.Type,
			summary.RetryCount,
			summary.BodySize,
			summary.Timestamp.Format("2006-01-02 15:04:05"),
		)
	}
	return writer.Flush()
}

func runReplay(
	ctx context.Context,
	cfg config.Config,
	args []string,
	stdout, stderr io.Writer,
	logger *slog.Logger,
) error {
	flags := flag.NewFlagSet("replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	messageID := flags.String("message-id", "", "exact RabbitMQ MessageId to replay")
	execute := flags.Bool("execute", false, "publish the message; without this flag only a dry run is performed")
	scanLimit := flags.Int(
		"scan-limit",
		defaultReplayScanLimit,
		"maximum number of DLQ messages to inspect",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *messageID == "" {
		return fmt.Errorf("replay: -message-id must not be empty")
	}

	session, err := openDLQSession(ctx, cfg.RabbitMQ, logger)
	if err != nil {
		return err
	}
	summary, found, replayErr := replayDeadLetter(
		ctx,
		cfg.RabbitMQ,
		session.channel,
		session.producer,
		*messageID,
		*execute,
		*scanLimit,
	)
	closeErr := session.Close()
	if replayErr != nil {
		return replayErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !found {
		return fmt.Errorf("replay: message %q was not found", *messageID)
	}

	if *execute {
		fmt.Fprintf(
			stdout,
			"replayed message_id=%s type=%s original_retry_count=%d\n",
			summary.MessageID,
			summary.Type,
			summary.RetryCount,
		)
		return nil
	}

	fmt.Fprintf(
		stdout,
		"DRY RUN: message_id=%s type=%s original_retry_count=%d would be replayed; rerun with -execute\n",
		summary.MessageID,
		summary.Type,
		summary.RetryCount,
	)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  dlqctl list [-limit 20]")
	fmt.Fprintln(w, "  dlqctl replay -message-id <id> [-scan-limit 1000]")
	fmt.Fprintln(w, "  dlqctl replay -message-id <id> -execute")
}
