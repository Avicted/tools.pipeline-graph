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
    teamcity_project_id: Platform_Base
formats: [drawio, html]
`), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	if c.Repos[0].Name != "service-a" || c.Repos[1].Name != "base" || c.Repos[1].Branch != "develop" || c.Repos[1].ProjectID != "Platform_Base" {
		t.Errorf("repos: %+v", c.Repos)
	}
	if !c.Has("html") || c.Has("dot") || len(c.Views) != 3 || c.Repos[0].TeamCityDir != ".teamcity" {
		t.Errorf("defaults: %+v", c)
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
	if _, err := Load(f); err == nil {
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
	c := &Config{Repos: []Repo{{URL: "git@h:o/a.git"}}, CacheDir: "~/pg-cache", TeamCityDir: "ci"}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if c.CacheDir != filepath.Join(home, "pg-cache") || c.OutDir != "out" || c.Concurrency != 4 || c.Repos[0].TeamCityDir != "ci" {
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
