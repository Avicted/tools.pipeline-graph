package ci

import (
	"strings"
	"testing"
)

type fake struct {
	name   string
	detect bool
}

func (f fake) Name() string                  { return f.name }
func (f fake) Detect(Repo) bool              { return f.detect }
func (f fake) Load(Repo) (*Pipelines, error) { return &Pipelines{}, nil }

func names(ps []Provider) string {
	var n []string
	for _, p := range ps {
		n = append(n, p.Name())
	}
	return strings.Join(n, ",")
}

func TestPick(t *testing.T) {
	all := []Provider{fake{"teamcity", true}, fake{"github", true}, fake{"gitlab", false}}
	for _, tc := range []struct {
		name, want, err string
	}{
		{"", "teamcity,github", ""}, // detection runs every system the repo uses
		{"gitlab", "gitlab", ""},    // an explicit choice skips detection
		{"github", "github", ""},
		{"jenkins", "", `unknown provider "jenkins" (known: teamcity, github, gitlab)`},
	} {
		got, err := Pick(all, tc.name, Repo{})
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("Pick(%q) error = %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || names(got) != tc.want {
			t.Errorf("Pick(%q) = %q, %v; want %q", tc.name, names(got), err, tc.want)
		}
	}
}

func TestPickNothingDetected(t *testing.T) {
	got, err := Pick([]Provider{fake{"teamcity", false}}, "", Repo{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}
