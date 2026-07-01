package signals_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/grackleclub/signals"
	"github.com/pterm/pterm"
)

// TestShowcase prints every console feature documented in examples.md so the
// output can be eyeballed end to end:
//
//	./bin/test showcase
//
// It is gated on SIGNALS_SHOWCASE so the noise (tables, spinners, boxes) stays
// out of the normal unit run; bin/test sets it. Interactive prompts block on a
// TTY and are left to examples.md, not run here.
func TestShowcase(t *testing.T) {
	if os.Getenv("SIGNALS_SHOWCASE") == "" {
		t.Skip("set SIGNALS_SHOWCASE=1 (or run bin/test showcase) to print the showcase")
	}
	ctx := context.Background()

	pterm.DefaultHeader.WithFullWidth().Println("signals console showcase")

	pterm.DefaultSection.Println("1. structured logging")
	log := signals.Logger(signals.Config{StderrLevel: slog.LevelDebug}, nil)
	log.DebugContext(ctx, "fine-grained detail", "count", 1)
	log.InfoContext(ctx, "listening", "addr", ":8080", "tls", true)
	log.WarnContext(ctx, "retrying", "attempt", 3, slog.Group("backoff", "ms", 250))
	log.With("component", "demo").InfoContext(ctx, "bound attrs accumulate", "a", 1)

	pterm.DefaultSection.Println("2. message lines")
	pterm.Info.Println("starting migration")
	pterm.Success.Println("migration complete")
	pterm.Warning.Println("no backup configured")
	pterm.Error.Println("rollback failed")

	pterm.DefaultSection.Println("3. tables")
	_ = pterm.DefaultTable.WithHasHeader().WithData(pterm.TableData{
		{"service", "status", "p99"},
		{"api", "ok", "42ms"},
		{"worker", "degraded", "910ms"},
	}).Render()

	pterm.DefaultSection.Println("4. boxes")
	pterm.DefaultBox.WithTitle("notice").WithTitleTopCenter().Println("deploy finished in 12s")

	pterm.DefaultSection.Println("5. headers (see top)")

	pterm.DefaultSection.Println("6. sections (these dividers)")

	pterm.DefaultSection.Println("7. bullet lists")
	_ = pterm.DefaultBulletList.WithItems([]pterm.BulletListItem{
		{Level: 0, Text: "build"},
		{Level: 1, Text: "compile"},
		{Level: 1, Text: "vet"},
		{Level: 0, Text: "deploy"},
	}).Render()

	pterm.DefaultSection.Println("8. trees")
	_ = pterm.DefaultTree.WithRoot(pterm.TreeNode{
		Text: "cloud",
		Children: []pterm.TreeNode{
			{Text: "api"},
			{Text: "worker", Children: []pterm.TreeNode{{Text: "queue"}}},
		},
	}).Render()

	pterm.DefaultSection.Println("9. spinners")
	sp, _ := pterm.DefaultSpinner.Start("connecting")
	sp.Success("connected")

	pterm.DefaultSection.Println("10. progress bars")
	bar, _ := pterm.DefaultProgressbar.WithTotal(3).WithTitle("uploading").Start()
	for range 3 {
		bar.Increment()
	}

	pterm.DefaultSection.Println("11. paragraphs")
	pterm.DefaultParagraph.Println(
		"signals bootstraps logs, metrics, and traces from one Setup call and " +
			"exports them over OTLP. With no endpoint configured it degrades to " +
			"this console only.")

	pterm.DefaultSection.Println("12. inline styling")
	pterm.Info.Println("status:", pterm.FgGreen.Sprint("healthy"))
	pterm.NewStyle(pterm.FgWhite, pterm.Bold).Println("emphasized")
}
