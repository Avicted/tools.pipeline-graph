package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndFinalize(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte(`
repos:
  - git@github.com:acme/service-a.git
  - url: ssh://git@git.acme.io:7999/plat/base.git
    branch: develop
    teamcity: {project_id: Platform_Base}
formats: [drawio, html]
`), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	if c.Repos[0].Name != "service-a" || c.Repos[1].Name != "base" || c.Repos[1].Branch != "develop" {
		t.Errorf("repos: %+v", c.Repos)
	}
	if !c.Has("html") || c.Has("dot") || len(c.Views) != 3 {
		t.Errorf("defaults: %+v", c)
	}
	var s tcSettings
	if err := c.Settings(1, "teamcity", &s); err != nil || s != (tcSettings{ProjectID: "Platform_Base"}) {
		t.Errorf("settings: %+v, %v", s, err)
	}
}

// tcSettings stands in for a provider's settings.
type tcSettings struct {
	Dir       string `yaml:"dir"`
	ProjectID string `yaml:"project_id"`
}

func load(t *testing.T, yml string) *Config {
	t.Helper()
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte(yml), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSettingsRepoBlockOverridesGlobalBlock(t *testing.T) {
	c := load(t, `
teamcity: {dir: ci/tc, project_id: Acme}
repos:
  - a/x.git
  - {url: b/y.git, teamcity: {dir: .tc}}
`)
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	var a, b tcSettings
	if err := c.Settings(0, "teamcity", &a); err != nil {
		t.Fatal(err)
	}
	if err := c.Settings(1, "teamcity", &b); err != nil {
		t.Fatal(err)
	}
	if a != (tcSettings{"ci/tc", "Acme"}) || b != (tcSettings{".tc", "Acme"}) {
		t.Errorf("global then repo: %+v, %+v", a, b)
	}
	var none tcSettings
	if err := c.Settings(0, "gitlab", &none); err != nil || none != (tcSettings{}) {
		t.Errorf("a provider without a block gets zero settings: %+v, %v", none, err)
	}
}

func TestSettingsTypoInsideBlock(t *testing.T) {
	c := load(t, "repos:\n  - {url: a/x.git, teamcity: {dri: ci}}\n")
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	var s tcSettings
	if err := c.Settings(0, "teamcity", &s); err == nil || !strings.Contains(err.Error(), "repos[0]: teamcity") {
		t.Errorf("a typo inside a block must say where: %v", err)
	}
}

func TestCheckBlocks(t *testing.T) {
	for yml, want := range map[string]string{
		"repos: [a/x.git]\nteamcty: {dir: x}\n":            "config: unknown field \"teamcty\" (provider settings: teamcity, jenkins)",
		"repos:\n  - {url: a/x.git, brnach: main}\n":       "repos[0]: unknown field \"brnach\"",
		"repos:\n  - {url: a/x.git, teamcity: {dir: x}}\n": "",
		"teamcity: {dir: x}\nrepos: [a/x.git]\n":           "",
	} {
		c := load(t, yml)
		err := c.CheckBlocks([]string{"teamcity", "jenkins"})
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("%q: want %q, got %v", yml, want, err)
		}
	}
}

func TestLegacyKeysFoldIntoBlocks(t *testing.T) {
	c := load(t, `
teamcity_dir: ci/tc
repos:
  - {url: a/x.git, teamcity_project_id: Acme_X}
  - {url: b/y.git, teamcity: {dir: .tc}, teamcity_project_id: Acme_Y}
`)
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckBlocks([]string{"teamcity"}); err != nil {
		t.Fatal(err)
	}
	var a, b tcSettings
	if err := c.Settings(0, "teamcity", &a); err != nil {
		t.Fatal(err)
	}
	if err := c.Settings(1, "teamcity", &b); err != nil {
		t.Fatal(err)
	}
	if a != (tcSettings{"ci/tc", "Acme_X"}) || b != (tcSettings{".tc", "Acme_Y"}) {
		t.Errorf("legacy keys must behave as the block: %+v, %+v", a, b)
	}
	if len(c.Notices) != 3 || !strings.Contains(strings.Join(c.Notices, "\n"), "repos[0]: teamcity_project_id is deprecated, write teamcity: {project_id: Acme_X}") {
		t.Errorf("each deprecated key is announced: %q", c.Notices)
	}
}

func TestLegacyAndNewKeyTogetherIsAnError(t *testing.T) {
	for _, yml := range []string{
		"teamcity_dir: a\nteamcity: {dir: b}\nrepos: [a/x.git]\n",
		"repos:\n  - {url: a/x.git, teamcity_dir: a, teamcity: {dir: b}}\n",
	} {
		if err := load(t, yml).Finalize(); err == nil || !strings.Contains(err.Error(), "both set") {
			t.Errorf("%q: %v", yml, err)
		}
	}
}

func TestValidation(t *testing.T) {
	for _, c := range []*Config{
		{},
		{Repos: []Repo{{URL: "a/x.git"}, {URL: "b/x.git"}}},
		{Repos: []Repo{{URL: "a"}}, Formats: []string{"png"}},
	} {
		if err := c.Finalize(); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestUnknownField(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("repo: [x]\n"), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	// a key that is no field is taken for a provider's block until it is checked against the real ones
	if err := c.CheckBlocks([]string{"teamcity"}); err == nil {
		t.Error("typo in key must be reported")
	}
}

func TestLocalPathsRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.yaml")
	os.WriteFile(f, []byte("repos:\n  - repos/a\n  - git@github.com:acme/b.git\n  - /abs/c\n"), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Repos[0].URL != filepath.Join(dir, "repos/a") || c.Repos[1].URL != "git@github.com:acme/b.git" || c.Repos[2].URL != "/abs/c" {
		t.Errorf("urls: %+v", c.Repos)
	}
}

func TestNameFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/service-a.git":        "service-a",
		"git@github.com:service-a.git":             "service-a", // no org
		"ssh://git@git.acme.io:7999/plat/base.git": "base",
		"ssh://git@git.acme.io/plat/base":          "base",
		"https://github.com/acme/web.git":          "web",
		"https://github.com/acme/web/":             "web",
		"file:///srv/git/svc.git":                  "svc",
		"../repos/release-train":                   "release-train",
	} {
		if got := NameFromURL(in); got != want {
			t.Errorf("NameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSHURLList(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte(`
repos:
  - git@github.com:acme/service-a.git
  - git@github.com:acme/service-b.git
  - ssh://git@git.acme.io:7999/plat/base.git
`), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range c.Repos {
		names = append(names, r.Name)
		if r.URL == "" || r.Provider != "" {
			t.Errorf("ssh urls are kept verbatim and detect their provider: %+v", r)
		}
	}
	if got := strings.Join(names, ","); got != "service-a,service-b,base" {
		t.Errorf("names: %s", got)
	}
}

func TestProviderField(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("repos:\n  - url: git@h:o/a.git\n    provider: github\n  - git@h:o/b.git\n"), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Repos[0].Provider != "github" || c.Repos[1].Provider != "" {
		t.Errorf("repos: %+v", c.Repos)
	}
}

func TestFinalizeDefaultsAndErrors(t *testing.T) {
	c := &Config{Repos: []Repo{{URL: "git@h:o/a.git"}}, CacheDir: "~/pg-cache"}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if c.CacheDir != filepath.Join(home, "pg-cache") || c.OutDir != "out" || c.Concurrency != 4 {
		t.Errorf("defaults: %+v", c)
	}
	d := &Config{Repos: []Repo{{URL: "a"}}}
	if err := d.Finalize(); err != nil || d.CacheDir == "" {
		t.Errorf("cache dir default: %v %q", err, d.CacheDir)
	}
	for name, bad := range map[string]*Config{
		"view":    {Repos: []Repo{{URL: "a"}}, Views: []string{"huge"}},
		"no url":  {Repos: []Repo{{Name: "x"}}},
		"missing": {},
	} {
		if err := bad.Finalize(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing config file must be an error")
	}
	if c, err := Load(""); err != nil || len(c.Repos) != 0 {
		t.Errorf("no config file is fine: %v", err)
	}
}

func TestCacheDirTildeNeedsAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	c := &Config{CacheDir: "~/pg-cache"}
	err := c.Finalize()
	if err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Errorf("a ~ that cannot be expanded must be an error, not the relative path %q: %v", c.CacheDir, err)
	}
}
