package config

import (
	"os"
	"path/filepath"
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
