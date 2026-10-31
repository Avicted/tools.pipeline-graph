// Command pipeline-graph clones git repositories, statically analyses their CI
// pipelines (TeamCity Kotlin DSL, GitHub Actions workflows, ...) and Makefiles,
// and renders a map of how all pipelines link together.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Avicted/tools.pipeline-graph/internal/analyze"
	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/config"
	"github.com/Avicted/tools.pipeline-graph/internal/fetch"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/render"
)

var version = "dev"

func main() {
	// Ctrl-C or SIGTERM cancels the context: git and dot are stopped and nothing half-done is written.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// Exit statuses.
const (
	exitFailed      = 1 // a repository could not be fetched, or an output could not be written
	exitUsage       = 2 // the command line or the config is wrong
	exitStrict      = 3 // -strict, and the analysis produced warnings
	exitInterrupted = 130
)

// interrupted reports, and says so on stderr, that the run was cancelled.
func interrupted(ctx context.Context, stderr io.Writer) bool {
	if ctx.Err() == nil {
		return false
	}
	fmt.Fprintln(stderr, "pipeline-graph: interrupted")
	return true
}

// options are the command-line flags.
type options struct {
	config  string
	out     string
	offline bool
	strict  bool
}

// run is the whole program: configure, fetch, analyse, write. Each phase says what went wrong
// on stderr itself and hands back the exit status when the run cannot go on.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, urls, code, done := parseArgs(args, stdout, stderr)
	if done {
		return code
	}
	cfg, settings, ok := configure(opts, urls, stderr)
	if !ok {
		return exitUsage
	}
	repos, failed, code := fetchAll(ctx, cfg, settings, opts.offline, stderr)
	if code != 0 {
		return code
	}
	g, warnings := analyze.Run(repos, analyze.Options{Providers: providers, ExternalIDs: cfg.ExternalIDs})
	g.Generated = generatedAt()
	if interrupted(ctx, stderr) {
		return exitInterrupted
	}
	if !write(ctx, cfg, g, warnings, stdout, stderr) {
		return exitFailed
	}
	summary(stderr, cfg, g, len(repos), len(warnings))
	switch {
	case failed > 0:
		return exitFailed
	case opts.strict && len(warnings) > 0:
		return exitStrict
	}
	return 0
}

// parseArgs reads the flags. done is set, with the exit status, when there is nothing to run
// (-version, or a bad flag).
func parseArgs(args []string, stdout, stderr io.Writer) (opts options, urls []string, code int, done bool) {
	fs := flag.NewFlagSet("pipeline-graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.config, "config", "", "YAML config file (see pipeline-graph.example.yaml)")
	fs.StringVar(&opts.out, "out", "", "output directory (overrides out_dir)")
	fs.BoolVar(&opts.offline, "offline", false, "do not fetch; use cached checkouts")
	fs.BoolVar(&opts.strict, "strict", false, "exit with status 3 when the analysis produced warnings")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: pipeline-graph [-config file.yaml] [-out dir] [-offline] [-strict] [repo-url ...]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return opts, nil, exitUsage, true
	}
	if *showVersion {
		fmt.Fprintln(stdout, "pipeline-graph", version)
		return opts, nil, 0, true
	}
	return opts, fs.Args(), 0, false
}

// configure loads and checks the config, with the URLs from the command line added. settings
// holds, per repo, what each configurable provider reads from its `<name>:` blocks.
func configure(opts options, urls []string, stderr io.Writer) (cfg *config.Config, settings []map[string]any, ok bool) {
	fail := func(err error) (*config.Config, []map[string]any, bool) {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return nil, nil, false
	}
	cfg, err := config.Load(opts.config)
	if err != nil {
		return fail(err)
	}
	for _, u := range urls {
		cfg.Repos = append(cfg.Repos, config.Repo{URL: u})
	}
	if opts.out != "" {
		cfg.OutDir = opts.out
	}
	if err := cfg.Finalize(); err != nil {
		return fail(err)
	}
	for _, r := range cfg.Repos {
		if r.Provider == "" {
			continue // detected per checkout, once there is one
		}
		if _, err := ci.Pick(providers, r.Provider, ci.Repo{}); err != nil {
			return fail(fmt.Errorf("%s: %w", r.Name, err))
		}
	}
	if err := cfg.CheckBlocks(settingsBlocks()); err != nil {
		return fail(err)
	}
	settings = make([]map[string]any, len(cfg.Repos))
	for i := range cfg.Repos {
		if settings[i], err = providerSettings(cfg, i); err != nil {
			return fail(err)
		}
	}
	for _, n := range cfg.Notices {
		fmt.Fprintln(stderr, "pipeline-graph: warning:", n)
	}
	return cfg, settings, true
}

// fetchAll clones or refreshes every repository. code is the exit status when the run cannot
// go on (interrupted, or nothing was fetched); failed counts the repositories that were left out.
func fetchAll(ctx context.Context, cfg *config.Config, settings []map[string]any, offline bool, stderr io.Writer) (repos []analyze.Repo, failed, code int) {
	reqs := make([]fetch.Request, len(cfg.Repos))
	for i, r := range cfg.Repos {
		reqs[i] = fetch.Request{URL: r.URL, Branch: r.Branch}
	}
	fmt.Fprintf(stderr, "pipeline-graph: fetching %d repositories into %s\n", len(reqs), cfg.CacheDir)
	results := fetch.All(ctx, reqs, cfg.CacheDir, cfg.Concurrency, offline)
	if interrupted(ctx, stderr) {
		return nil, 0, exitInterrupted
	}
	for i, res := range results {
		r := cfg.Repos[i]
		if res.Err != nil {
			failed++
			fmt.Fprintf(stderr, "pipeline-graph: %s: %v\n", r.Name, res.Err)
			continue
		}
		repos = append(repos, analyze.Repo{Name: r.Name, URL: r.URL, Commit: res.Commit, Root: res.Dir, Provider: r.Provider, Settings: settings[i]})
	}
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "pipeline-graph: no repository could be fetched")
		return nil, failed, exitFailed
	}
	return repos, failed, 0
}

// write builds the views and writes every output, listing the files on stdout. It says what
// failed on stderr and reports whether everything was written.
func write(ctx context.Context, cfg *config.Config, g *model.Graph, warnings []string, stdout, stderr io.Writer) bool {
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return false
	}
	views := render.BuildViews(g, cfg.Views)
	var vsdxViews []render.View
	if cfg.Has("vsdx") {
		vsdxViews = render.BuildViewsSized(g, cfg.Views, render.SizeVSDX)
	}
	g.Warnings = warnings
	written, err := writeOutputs(ctx, cfg, g, views, vsdxViews, warnings)
	for _, f := range written {
		fmt.Fprintln(stdout, f)
	}
	if err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return false
	}
	return true
}

// summary is the one line that says what the run found.
func summary(stderr io.Writer, cfg *config.Config, g *model.Graph, repos, warnings int) {
	counts := map[model.NodeKind]int{}
	for _, n := range g.Nodes {
		counts[n.Kind]++
	}
	fmt.Fprintf(stderr, "pipeline-graph: %d repos, %d jobs, %d make targets, %d images, %d external, %d links, %d warnings (see %s)\n",
		repos, counts[model.KindBuild], counts[model.KindMake], counts[model.KindImage], counts[model.KindExternal],
		len(g.Edges), warnings, filepath.Join(cfg.OutDir, "warnings.txt"))
}
