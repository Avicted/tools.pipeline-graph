// Package config loads the pipeline-graph YAML configuration.
package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Repo is one repository. A key that is not listed here is a provider's settings
// block, `<provider name>: {...}`, kept in Blocks for Settings to decode.
type Repo struct {
	URL      string `yaml:"url"`
	Name     string `yaml:"name"`     // lane title; default: last URL path segment without .git
	Branch   string `yaml:"branch"`   // default: remote HEAD
	Provider string `yaml:"provider"` // CI system to read (e.g. teamcity); default: every supported one found in the repo

	// Deprecated spellings of teamcity: {dir, project_id}; Finalize folds them into Blocks.
	LegacyDir       string `yaml:"teamcity_dir"`
	LegacyProjectID string `yaml:"teamcity_project_id"`

	Blocks map[string]yaml.Node `yaml:",inline"`
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

// Config is the whole file. Besides the fields below, a top-level `<provider name>:`
// block holds that provider's settings for every repo; a repo's own block overrides it.
type Config struct {
	Repos       []Repo            `yaml:"repos"`
	CacheDir    string            `yaml:"cache_dir"`
	OutDir      string            `yaml:"out_dir"`
	Views       []string          `yaml:"views"`
	Formats     []string          `yaml:"formats"`
	ExternalIDs map[string]string `yaml:"external_ids"` // a job's id in its CI system -> "repo/Key"
	Concurrency int               `yaml:"concurrency"`

	LegacyDir string `yaml:"teamcity_dir"` // deprecated spelling of teamcity: {dir}

	Blocks map[string]yaml.Node `yaml:",inline"`

	// Notices are things worth telling the user that are not errors, e.g. deprecated keys.
	Notices []string `yaml:"-"`
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
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cache_dir %q: cannot find your home directory: %w", c.CacheDir, err)
		}
		c.CacheDir = filepath.Join(home, c.CacheDir[2:])
	}
	if c.OutDir == "" {
		c.OutDir = "out"
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
	if c.LegacyDir != "" {
		if err := foldLegacy(&c.Blocks, "teamcity", "dir", c.LegacyDir, "teamcity_dir", "config", &c.Notices); err != nil {
			return err
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
		if err := r.foldLegacy(fmt.Sprintf("repos[%d]", i), &c.Notices); err != nil {
			return err
		}
		if other, dup := seen[r.Name]; dup {
			return fmt.Errorf("repos %s and %s both have name %q; set name: explicitly", other, r.URL, r.Name)
		}
		seen[r.Name] = r.URL
	}
	return nil
}

func (r *Repo) foldLegacy(where string, notices *[]string) error {
	for _, l := range []struct{ old, key, val string }{
		{"teamcity_dir", "dir", r.LegacyDir},
		{"teamcity_project_id", "project_id", r.LegacyProjectID},
	} {
		if l.val == "" {
			continue
		}
		if err := foldLegacy(&r.Blocks, "teamcity", l.key, l.val, l.old, where, notices); err != nil {
			return err
		}
	}
	return nil
}

// foldLegacy moves a deprecated top-level key into block.key, refusing a block that also sets it.
func foldLegacy(blocks *map[string]yaml.Node, block, key, val, old, where string, notices *[]string) error {
	m := map[string]any{}
	if n, ok := (*blocks)[block]; ok {
		if err := n.Decode(&m); err != nil {
			return fmt.Errorf("%s: %s: %w", where, block, err)
		}
	}
	if _, dup := m[key]; dup {
		return fmt.Errorf("%s: %s and %s.%s are both set; drop %s", where, old, block, key, old)
	}
	m[key] = val
	var n yaml.Node
	if err := n.Encode(m); err != nil {
		return err
	}
	if *blocks == nil {
		*blocks = map[string]yaml.Node{}
	}
	(*blocks)[block] = n
	*notices = append(*notices, fmt.Sprintf("%s: %s is deprecated, write %s: {%s: %s}", where, old, block, key, val))
	return nil
}

// CheckBlocks rejects a top-level or repo key that is neither a config field nor
// a settings block of one of the providers in known (the names of those with settings).
func (c *Config) CheckBlocks(known []string) error {
	check := func(where string, blocks map[string]yaml.Node) error {
		for _, k := range sortedKeys(blocks) {
			if !contains(known, k) {
				return fmt.Errorf("%s: unknown field %q (provider settings: %s)", where, k, strings.Join(known, ", "))
			}
		}
		return nil
	}
	if err := check("config", c.Blocks); err != nil {
		return err
	}
	for i, r := range c.Repos {
		if err := check(fmt.Sprintf("repos[%d]", i), r.Blocks); err != nil {
			return err
		}
	}
	return nil
}

// Settings decodes provider name's settings for repo i into v: the top-level block
// first, then the repo's own on top of it. Unknown keys inside a block are errors.
func (c *Config) Settings(i int, name string, v any) error {
	for _, b := range []struct {
		where  string
		blocks map[string]yaml.Node
	}{{"config", c.Blocks}, {fmt.Sprintf("repos[%d]", i), c.Repos[i].Blocks}} {
		n, ok := b.blocks[name]
		if !ok {
			continue
		}
		raw, err := yaml.Marshal(&n)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(v); err != nil {
			return fmt.Errorf("%s: %s: %w", b.where, name, err)
		}
	}
	return nil
}

func sortedKeys(m map[string]yaml.Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
