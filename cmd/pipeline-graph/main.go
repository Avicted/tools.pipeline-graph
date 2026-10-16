// Command pipeline-graph clones git repositories, statically analyses their
// TeamCity Kotlin DSL and Makefiles, and renders a map of how all pipelines link together.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pipeline-graph/internal/analyze"
	"pipeline-graph/internal/config"
	"pipeline-graph/internal/fetch"
	"pipeline-graph/internal/model"
	"pipeline-graph/internal/render"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
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

	// 1. fetch
	reqs := make([]fetch.Request, len(cfg.Repos))
	for i, r := range cfg.Repos {
		reqs[i] = fetch.Request{URL: r.URL, Branch: r.Branch}
	}
	fmt.Fprintf(stderr, "pipeline-graph: fetching %d repositories into %s\n", len(reqs), cfg.CacheDir)
	results := fetch.All(context.Background(), reqs, cfg.CacheDir, cfg.Concurrency, *offline)
	var repos []analyze.Repo
	failed := 0
	for i, res := range results {
		r := cfg.Repos[i]
		if res.Err != nil {
			failed++
			fmt.Fprintf(stderr, "pipeline-graph: %s: %v\n", r.Name, res.Err)
			continue
		}
		repos = append(repos, analyze.Repo{Name: r.Name, URL: r.URL, Commit: res.Commit, Root: res.Dir, TeamCityDir: r.TeamCityDir, ProjectID: r.ProjectID})
	}
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "pipeline-graph: no repository could be fetched")
		return 1
	}

	// 2. analyse
	g, warnings := analyze.Run(repos, analyze.Options{ExternalIDs: cfg.ExternalIDs})
	g.Generated = generatedAt()

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
	written, err := writeOutputs(cfg, g, views, vsdxViews, warnings)
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
	fmt.Fprintf(stderr, "pipeline-graph: %d repos, %d build configurations, %d make targets, %d images, %d external, %d links, %d warnings (see %s)\n",
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

func writeOutputs(cfg *config.Config, g *model.Graph, views, vsdxViews []render.View, warnings []string) ([]string, error) {
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
		{"html", "pipelines.html", func(w io.Writer) error { return render.HTML(w, g, views, warnings) }},
		{"mermaid", "pipelines.mmd", func(w io.Writer) error { return render.Mermaid(w, overview(g)) }},
		{"dot", "pipelines.dot", func(w io.Writer) error { return render.DOT(w, overview(g)) }},
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
			if out, err := exec.Command("dot", "-Tsvg", dotFile, "-o", svgFile).CombinedOutput(); err != nil {
				return written, fmt.Errorf("dot -Tsvg: %w: %s", err, out)
			}
			written = append(written, svgFile)
		}
	}
	return written, nil
}

// overview drops make targets: the text formats are meant for READMEs and stay readable.
func overview(g *model.Graph) *model.Graph { return render.WithoutMake(g) }
