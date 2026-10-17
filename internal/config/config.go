// Package config loads the pipeline-graph YAML configuration.
package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Repo struct {
	URL         string `yaml:"url"`
	Name        string `yaml:"name"`                // lane title; default: last URL path segment without .git
	Branch      string `yaml:"branch"`              // default: remote HEAD
	TeamCityDir string `yaml:"teamcity_dir"`        // default: top-level teamcity_dir
	ProjectID   string `yaml:"teamcity_project_id"` // TeamCity project the settings are imported into; default derived from name
	Provider    string `yaml:"provider"`            // CI system to read (e.g. teamcity); default: every supported one found in the repo
}

// UnmarshalYAML accepts either a mapping or a bare URL string.
func (r *Repo) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.URL = n.Value
		return nil
	}
	type plain Repo
	return n.Decode((*plain)(r))
}

type Config struct {
	Repos       []Repo            `yaml:"repos"`
	CacheDir    string            `yaml:"cache_dir"`
	OutDir      string            `yaml:"out_dir"`
	TeamCityDir string            `yaml:"teamcity_dir"`
	Views       []string          `yaml:"views"`
	Formats     []string          `yaml:"formats"`
	ExternalIDs map[string]string `yaml:"external_ids"` // absolute TeamCity id -> "repo/RelativeId"
	Concurrency int               `yaml:"concurrency"`
}

var (
	AllViews   = []string{"overview", "full", "per-repo"}
	AllFormats = []string{"drawio", "vsdx", "html", "mermaid", "dot", "json"}
)

func Load(file string) (*Config, error) {
	c := &Config{}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		// local checkouts listed in the config are relative to the config file
		for i, r := range c.Repos {
			if isRelativePath(r.URL) {
				c.Repos[i].URL = filepath.Join(filepath.Dir(file), r.URL)
			}
		}
	}
	return c, nil
}

// Finalize applies defaults and validates.
func (c *Config) Finalize() error {
	if c.CacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		c.CacheDir = filepath.Join(base, "pipeline-graph")
	}
	if strings.HasPrefix(c.CacheDir, "~/") {
		home, _ := os.UserHomeDir()
		c.CacheDir = filepath.Join(home, c.CacheDir[2:])
	}
	if c.OutDir == "" {
		c.OutDir = "out"
	}
	if c.TeamCityDir == "" {
		c.TeamCityDir = ".teamcity"
	}
	if len(c.Views) == 0 {
		c.Views = AllViews
	}
	if len(c.Formats) == 0 {
		c.Formats = AllFormats
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("no repositories configured (add repos: to the config or pass URLs as arguments)")
	}
	for _, v := range c.Views {
		if !contains(AllViews, v) {
			return fmt.Errorf("unknown view %q (valid: %s)", v, strings.Join(AllViews, ", "))
		}
	}
	for _, f := range c.Formats {
		if !contains(AllFormats, f) {
			return fmt.Errorf("unknown format %q (valid: %s)", f, strings.Join(AllFormats, ", "))
		}
	}
	seen := map[string]string{}
	for i := range c.Repos {
		r := &c.Repos[i]
		if r.URL == "" {
			return fmt.Errorf("repos[%d]: url is required", i)
		}
		if r.Name == "" {
			r.Name = NameFromURL(r.URL)
		}
		if r.TeamCityDir == "" {
			r.TeamCityDir = c.TeamCityDir
		}
		if other, dup := seen[r.Name]; dup {
			return fmt.Errorf("repos %s and %s both have name %q; set name: explicitly", other, r.URL, r.Name)
		}
		seen[r.Name] = r.URL
	}
	return nil
}

// NameFromURL turns git@host:org/service-a.git or https://host/org/service-a into "service-a".
func NameFromURL(u string) string {
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndexAny(u, ":/"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(path.Base(u), ".git")
}

// isRelativePath reports whether a repo entry is a relative local path rather
// than a git URL (scheme://..., user@host:path).
func isRelativePath(u string) bool {
	return u != "" && !filepath.IsAbs(u) && !strings.Contains(u, "://") && !strings.Contains(u, "@") && !strings.HasPrefix(u, "~")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (c *Config) Has(format string) bool { return contains(c.Formats, format) }
