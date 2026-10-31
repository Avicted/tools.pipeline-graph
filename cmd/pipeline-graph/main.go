// Command pipeline-graph clones git repositories, statically analyses their CI
// pipelines (TeamCity Kotlin DSL, GitHub Actions workflows, ...) and Makefiles,
// and renders a map of how all pipelines link together.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Avicted/tools.pipeline-graph/internal/analyze"
	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/github"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/teamcity"
	"github.com/Avicted/tools.pipeline-graph/internal/config"
	"github.com/Avicted/tools.pipeline-graph/internal/fetch"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/render"
)

var version = "dev"

// dotTimeout is how long Graphviz gets to draw the SVG.
var dotTimeout = 2 * time.Minute

// providers are the CI/CD systems that can be read; add new ones here.
var providers = []ci.Provider{teamcity.New(), github.New()}

// settingsBlocks are the names of the providers that take settings, which are the
// keys a config may have besides its own fields.
func settingsBlocks() []string {
	var names []string
	for _, p := range providers {
		if _, ok := p.(ci.Configurable); ok {
			names = append(names, p.System().Name)
		}
	}
	return names
}

// providerSettings decodes every configurable provider's settings for repo i.
func providerSettings(cfg *config.Config, i int) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range providers {
		if c, ok := p.(ci.Configurable); ok {
			v := c.NewSettings()
			if err := cfg.Settings(i, p.System().Name, v); err != nil {
				return nil, err
			}
			out[p.System().Name] = v
		}
	}
	return out, nil
}

func main() {
	// Ctrl-C or SIGTERM cancels the context: git and dot are stopped and nothing half-done is written.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// exitInterrupted is the conventional status of a run ended by SIGINT.
const exitInterrupted = 130

// interrupted reports, and says so on stderr, that the run was cancelled.
func interrupted(ctx context.Context, stderr io.Writer) bool {
	if ctx.Err() == nil {
		return false
	}
	fmt.Fprintln(stderr, "pipeline-graph: interrupted")
	return true
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline-graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFile := fs.String("config", "", "YAML config file (see pipeline-graph.example.yaml)")
	outDir := fs.String("out", "", "output directory (overrides out_dir)")
	offline := fs.Bool("offline", false, "do not fetch; use cached checkouts")
	strict := fs.Bool("strict", false, "exit with status 3 when the analysis produced warnings")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: pipeline-graph [-config file.yaml] [-out dir] [-offline] [-strict] [repo-url ...]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "pipeline-graph", version)
		return 0
	}
	cfg, err := config.Load(*cfgFile)
	if err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return 2
	}
	for _, u := range fs.Args() {
		cfg.Repos = append(cfg.Repos, config.Repo{URL: u})
	}
	if *outDir != "" {
		cfg.OutDir = *outDir
	}
	if err := cfg.Finalize(); err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return 2
	}

	for _, r := range cfg.Repos {
		if r.Provider == "" {
			continue // detected per checkout, once there is one
		}
		if _, err := ci.Pick(providers, r.Provider, ci.Repo{}); err != nil {
			fmt.Fprintf(stderr, "pipeline-graph: %s: %v\n", r.Name, err)
			return 2
		}
	}
	if err := cfg.CheckBlocks(settingsBlocks()); err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return 2
	}
	settings := make([]map[string]any, len(cfg.Repos))
	for i := range cfg.Repos {
		var err error
		if settings[i], err = providerSettings(cfg, i); err != nil {
			fmt.Fprintln(stderr, "pipeline-graph:", err)
			return 2
		}
	}
	for _, n := range cfg.Notices {
		fmt.Fprintln(stderr, "pipeline-graph: warning:", n)
	}

	// 1. fetch
	reqs := make([]fetch.Request, len(cfg.Repos))
	for i, r := range cfg.Repos {
		reqs[i] = fetch.Request{URL: r.URL, Branch: r.Branch}
	}
	fmt.Fprintf(stderr, "pipeline-graph: fetching %d repositories into %s\n", len(reqs), cfg.CacheDir)
	results := fetch.All(ctx, reqs, cfg.CacheDir, cfg.Concurrency, *offline)
	if interrupted(ctx, stderr) {
		return exitInterrupted
	}
	var repos []analyze.Repo
	failed := 0
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
		return 1
	}

	// 2. analyse
	g, warnings := analyze.Run(repos, analyze.Options{Providers: providers, ExternalIDs: cfg.ExternalIDs})
	g.Generated = generatedAt()
	if interrupted(ctx, stderr) {
		return exitInterrupted
	}

	// 3. render
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		fmt.Fprintln(stderr, "pipeline-graph:", err)
		return 1
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
		return 1
	}
	counts := map[model.NodeKind]int{}
	for _, n := range g.Nodes {
		counts[n.Kind]++
	}
	fmt.Fprintf(stderr, "pipeline-graph: %d repos, %d jobs, %d make targets, %d images, %d external, %d links, %d warnings (see %s)\n",
		len(repos), counts[model.KindBuild], counts[model.KindMake], counts[model.KindImage], counts[model.KindExternal],
		len(g.Edges), len(warnings), filepath.Join(cfg.OutDir, "warnings.txt"))
	switch {
	case failed > 0:
		return 1
	case *strict && len(warnings) > 0:
		return 3
	}
	return 0
}

// generatedAt honours SOURCE_DATE_EPOCH for reproducible output.
func generatedAt() string {
	t := time.Now().UTC()
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
			t = time.Unix(sec, 0).UTC()
		}
	}
	return t.Format("2006-01-02 15:04 UTC")
}

func writeOutputs(ctx context.Context, cfg *config.Config, g *model.Graph, views, vsdxViews []render.View, warnings []string) ([]string, error) {
	var written []string
	write := func(name string, fn func(io.Writer) error) error {
		var buf bytes.Buffer
		if err := fn(&buf); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		p := filepath.Join(cfg.OutDir, name)
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			return err
		}
		written = append(written, p)
		return nil
	}
	steps := []struct {
		format, file string
		fn           func(io.Writer) error
	}{
		{"drawio", "pipelines.drawio", func(w io.Writer) error { return render.DrawIO(w, g, views) }},
		{"vsdx", "pipelines.vsdx", func(w io.Writer) error { return render.VSDX(w, g, vsdxViews) }},
		{"mermaid", "pipelines.mmd", func(w io.Writer) error { return render.Mermaid(w, render.WithoutMake(g)) }},
		{"dot", "pipelines.dot", func(w io.Writer) error { return render.DOT(w, render.WithoutMake(g)) }},
		{"json", "graph.json", func(w io.Writer) error { return g.WriteJSON(w) }},
	}
	for _, s := range steps {
		if !cfg.Has(s.format) {
			continue
		}
		if err := write(s.file, s.fn); err != nil {
			return written, err
		}
	}
	if cfg.Has("json") {
		if p, err := writeHistory(cfg.OutDir, g); err != nil {
			return written, err
		} else if p != "" {
			written = append(written, p)
		}
	}
	if err := write("warnings.txt", func(w io.Writer) error {
		_, err := io.WriteString(w, strings.Join(warnings, "\n")+"\n")
		return err
	}); err != nil {
		return written, err
	}
	if cfg.Has("dot") {
		if _, err := exec.LookPath("dot"); err == nil {
			dotFile := filepath.Join(cfg.OutDir, "pipelines.dot")
			svgFile := filepath.Join(cfg.OutDir, "pipelines.svg")
			dctx, cancel := context.WithTimeout(ctx, dotTimeout)
			out, err := exec.CommandContext(dctx, "dot", "-Tsvg", dotFile, "-o", svgFile).CombinedOutput()
			timedOut := errors.Is(dctx.Err(), context.DeadlineExceeded)
			cancel()
			switch {
			case timedOut:
				return written, fmt.Errorf("dot -Tsvg: timed out after %s", dotTimeout)
			case err != nil:
				return written, fmt.Errorf("dot -Tsvg: %w: %s", err, out)
			}
			written = append(written, svgFile)
		}
	}
	return written, nil
}

// writeHistory keeps a copy of graph.json under <out>/history, named by time and
// content, so the viewer can compare against an earlier run. A run that found the
// same graph as an existing snapshot adds nothing; it returns "" then.
func writeHistory(outDir string, g *model.Graph) (string, error) {
	var buf bytes.Buffer
	if err := g.WriteJSON(&buf); err != nil {
		return "", err
	}
	stamped := *g
	stamped.Generated = ""
	var h bytes.Buffer
	if err := stamped.WriteJSON(&h); err != nil {
		return "", err
	}
	sum := sha256.Sum256(h.Bytes())
	tag := hex.EncodeToString(sum[:4])
	dir := filepath.Join(outDir, "history")
	if m, _ := filepath.Glob(filepath.Join(dir, "*-"+tag+".json")); len(m) > 0 {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ts := strings.NewReplacer(" ", "T", ":", "", "-", "").Replace(strings.TrimSuffix(g.Generated, " UTC")) + "Z"
	p := filepath.Join(dir, ts+"-"+tag+".json")
	return p, os.WriteFile(p, buf.Bytes(), 0o644)
}

// overview drops make targets: the text formats are meant for READMEs and stay readable.
