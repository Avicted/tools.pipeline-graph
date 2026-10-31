package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Avicted/tools.pipeline-graph/internal/config"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/render"
)

// dotTimeout is how long Graphviz gets to draw the SVG.
var dotTimeout = 2 * time.Minute

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
