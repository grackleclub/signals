# examples

The console sink is a [pterm](https://github.com/pterm/pterm) logger. pterm is a process-global singleton, so once anything in your build imports it the styling signals sets is shared everywhere — your tables, spinners, and boxes match your logs with no wiring. Import it directly and call the `Default*` printers.

```go
import "github.com/pterm/pterm"
```

Everything below writes to the same terminal signals already owns. None of it needs a handle from `Setup`; pterm holds its own globals.

## 1. structured logging

The `*slog.Logger` from `Setup` is the primary surface. Pass a `ctx` that carries a span and the console renders a short `trace_id` inline, while the OTLP sink ships the full trace context.

```go
shutdown, log, err := signals.Setup(ctx, signals.Config{Env: "prod"})
if err != nil {
	return fmt.Errorf("setup signals: %w", err)
}
defer shutdown(ctx)

log.InfoContext(ctx, "listening", "addr", ":8080", "tls", true)
log.WarnContext(ctx, "retrying", "attempt", 3)
log.ErrorContext(ctx, "upstream failed", "err", err)
```

Console tuning lives on `Config.Console`:

```go
signals.Setup(ctx, signals.Config{
	Console: signals.Console{Time: signals.TimeOff, Layout: signals.LayoutTree},
})
```

`Time` defaults to `TimeAuto`: the timestamp is hidden at an interactive terminal (local) and shown when stderr is captured (CI, a file, journald), since a captured log needs its own clock. Force it with `TimeOn` / `TimeOff`.

`Layout` defaults to `LayoutAuto`, which arranges args by where the output lands. On a terminal or in GitHub Actions it uses a tree — each arg on its own line, values aligned in a column, colon tight against each key:

```
INFO  listening
    ├ addr: :8080
    └ tls:  true
```

Everywhere else (journald, files, other line-oriented capture, where a multi-line record would fragment into separate entries) it keeps the record on one line:

```
13:37:00.220 INFO  listening addr: :8080 tls: true
```

Force either with `LayoutTree` / `LayoutOneline`.

## 2. message lines

Prefix printers for one-off status lines, colored and tagged. These are not the structured logger; reach for them in CLIs, not services.

```go
pterm.Info.Println("starting migration")
pterm.Success.Println("migration complete")
pterm.Warning.Println("no backup configured")
pterm.Error.Println("rollback failed")
```

## 3. tables

```go
data := pterm.TableData{
	{"service", "status", "p99"},
	{"api", "ok", "42ms"},
	{"worker", "degraded", "910ms"},
}
err := pterm.DefaultTable.WithHasHeader().WithData(data).Render()
```

`WithBoxed()` draws borders; `Srender()` returns the string instead of printing it.

## 4. boxes

```go
pterm.DefaultBox.
	WithTitle("notice").
	WithTitleTopCenter().
	Println("deploy finished in 12s")
```

## 5. headers

```go
pterm.DefaultHeader.WithFullWidth().Println("Release 1.4.0")
```

## 6. sections

A lighter divider for grouping output into stages.

```go
pterm.DefaultSection.Println("Preflight")
pterm.Info.Println("checking credentials")

pterm.DefaultSection.Println("Apply")
pterm.Success.Println("done")
```

## 7. bullet lists

```go
items := []pterm.BulletListItem{
	{Level: 0, Text: "build"},
	{Level: 1, Text: "compile"},
	{Level: 1, Text: "vet"},
	{Level: 0, Text: "deploy"},
}
err := pterm.DefaultBulletList.WithItems(items).Render()
```

## 8. trees

```go
tree := pterm.TreeNode{
	Text: "cloud",
	Children: []pterm.TreeNode{
		{Text: "api"},
		{Text: "worker", Children: []pterm.TreeNode{{Text: "queue"}}},
	},
}
err := pterm.DefaultTree.WithRoot(tree).Render()
```

`pterm.NewTreeFromLeveledList` builds the same from an indented `LeveledList`, but it promotes the first level-0 item to the root and also keeps it as a child, so the root prints twice — the explicit `TreeNode` avoids that.

## 9. spinners

For indeterminate work. Start it, then resolve with `Success` or `Fail`.

```go
sp, _ := pterm.DefaultSpinner.Start("connecting")
if err := dial(); err != nil {
	sp.Fail("connection refused")
} else {
	sp.Success("connected")
}
```

## 10. progress bars

For work with a known total.

```go
bar, _ := pterm.DefaultProgressbar.WithTotal(len(files)).WithTitle("uploading").Start()
for _, f := range files {
	upload(f)
	bar.Increment()
}
```

## 11. paragraphs

Reflows long prose to the terminal width instead of letting it run off the edge.

```go
pterm.DefaultParagraph.Println(
	"signals bootstraps logs, metrics, and traces from one Setup call and " +
		"exports them over OTLP. With no endpoint configured it degrades to " +
		"this console only.")
```

## 12. interactive prompts

Blocking TTY prompts for CLIs. `Show` returns the choice.

```go
choice, _ := pterm.DefaultInteractiveSelect.
	WithOptions([]string{"prod", "staging", "dev"}).
	Show("target environment")

ok, _ := pterm.DefaultInteractiveConfirm.Show("proceed with deploy")
if !ok {
	return nil
}
```

## styling text inline

Any of the above accepts pre-styled strings. Colors and styles are values you can `Sprint` with.

```go
pterm.Info.Println("status:", pterm.FgGreen.Sprint("healthy"))
bold := pterm.NewStyle(pterm.FgWhite, pterm.Bold)
bold.Println("emphasized")
```

## notes

- Color and layout track the same signal: on a terminal or in GitHub Actions (both render ANSI and multi-line) you get color and the tree; elsewhere (journald, files) output is stripped to plain and one line. `NO_COLOR` forces plain everywhere; `CLICOLOR_FORCE` keeps color even when captured.
- signals renders the console through its own slog handler rather than pterm's bundled bridge, so attribute order is preserved, open groups prefix their keys (`req.method`), and chained `.With` accumulates. pterm's own `NewSlogHandler` does none of these.
- Interactive prompts and live printers (spinner, progress bar) assume a TTY. They no-op gracefully into piped or captured output, so guard them behind a TTY check in non-interactive contexts if the plain fallback is noisy.
