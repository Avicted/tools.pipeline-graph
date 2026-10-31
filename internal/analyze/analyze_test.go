package analyze

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/teamcity"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

var tc = []ci.Provider{teamcity.New()}

func repo(t *testing.T, name string, files map[string]string) Repo {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Repo{Name: name, Root: root}
}

func edges(g *model.Graph) string {
	var s []string
	for _, e := range g.Edges {
		s = append(s, e.From+">"+e.To+":"+string(e.Kind))
	}
	return strings.Join(s, "\n")
}

func TestCrossRepoResolution(t *testing.T) {
	lib := repo(t, "core-lib", map[string]string{".teamcity/settings.kts": `
project { buildType(Publish) }
object Publish : BuildType({ name = "Publish" })
`})
	app := repo(t, "app", map[string]string{".teamcity/settings.kts": `
project { buildType(Build) }
object Build : BuildType({
    dependencies {
        snapshot(AbsoluteId("CoreLib_Publish")) {}             // derived project id
        snapshot(AbsoluteId("Acme_CoreLib_Publish")) {}        // nested under a parent project: suffix match
        snapshot(AbsoluteId("Other_Publish")) {}               // unrelated project: external
        artifacts(AbsoluteId("Mapped")) { artifactRules = "x" } // external_ids mapping
    }
})
`})
	g, warnings := Run([]Repo{lib, app}, Options{Providers: tc, ExternalIDs: map[string]string{"Mapped": "core-lib/Publish"}})
	want := strings.Join([]string{
		"bt:core-lib/Publish>bt:app/Build:artifact",
		"bt:core-lib/Publish>bt:app/Build:snapshot",
		"ext:Other_Publish>bt:app/Build:snapshot",
	}, "\n")
	if got := edges(g); got != want {
		t.Errorf("edges:\n%s\nwant:\n%s", got, want)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "Acme_CoreLib_Publish matched to bt:core-lib/Publish by id suffix") ||
		!strings.Contains(joined, "Other_Publish not found") {
		t.Errorf("warnings: %s", joined)
	}
}

func TestImagesAndMake(t *testing.T) {
	base := repo(t, "base", map[string]string{
		".teamcity/settings.kts": `
object Img : BuildType({
    params { param("img", "reg.io/base") }
    steps { script { scriptContent = "make image TAG=%build.number%" } }
})`,
		"Makefile": "image: check\n\tdocker build -t %img%:$(TAG) .\n\tdocker push reg.io/base:$(TAG)\ncheck:\n\t@true\n",
	})
	app := repo(t, "app", map[string]string{
		".teamcity/settings.kts": `
object App : BuildType({
    steps { script { scriptContent = "docker build -f build/Dockerfile ." } }
})`,
		"build/Dockerfile": "FROM reg.io/base:42\nFROM alpine:3\n",
	})
	g, _ := Run([]Repo{base, app}, Options{Providers: tc})
	want := strings.Join([]string{
		"bt:base/Img>mk:base/Makefile:image:runs",
		"img:reg.io/base>bt:app/App:consumes",
		"mk:base/Makefile:image>img:reg.io/base:produces",
		"mk:base/Makefile:image>mk:base/Makefile:check:depends",
	}, "\n")
	if got := edges(g); got != want {
		t.Errorf("edges:\n%s\nwant:\n%s", got, want)
	}
	if g.Node("img:alpine") != nil {
		t.Error("third-party images must not become nodes")
	}
}

func TestImageName(t *testing.T) {
	for in, want := range map[string]string{
		"reg.io/a/b:1.2":          "reg.io/a/b",
		"localhost:5000/x":        "localhost:5000/x",
		"reg.io/x@sha256:abc":     "reg.io/x",
		"reg.io/x:%build.number%": "reg.io/x",
		"$(IMAGE):latest":         "",
	} {
		if got := imageName(in); got != want {
			t.Errorf("imageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A repository is untrusted: what a script or Dockerfile path points at outside it must
// not be read, whether by "..", or by a symlink inside the checkout.
func TestFilesOutsideTheRepositoryAreNotRead(t *testing.T) {
	cases := map[string]struct {
		step    string // the TeamCity step
		symlink string // link name inside the repo, pointing at the outside file
		file    string // content of the outside file
		leaked  string // node that would exist had the file been read
	}{
		"script via ..":      {step: "sh ../outside.sh", file: "docker build -t reg.io/leaked:1 .\n", leaked: "img:reg.io/leaked"},
		"script via symlink": {step: "sh ci/run.sh", symlink: "ci/run.sh", file: "docker build -t reg.io/leaked:1 .\n", leaked: "img:reg.io/leaked"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.symlink != "" && runtime.GOOS == "windows" {
				t.Skip("symlinks need privileges on windows")
			}
			parent := t.TempDir()
			root := filepath.Join(parent, "repo")
			outside := filepath.Join(parent, "outside.sh")
			if err := os.WriteFile(outside, []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, ".teamcity"), 0o755); err != nil {
				t.Fatal(err)
			}
			settings := `object App : BuildType({ steps { script { scriptContent = "` + c.step + `" } } })`
			if err := os.WriteFile(filepath.Join(root, ".teamcity", "settings.kts"), []byte(settings), 0o644); err != nil {
				t.Fatal(err)
			}
			if c.symlink != "" {
				if err := os.MkdirAll(filepath.Join(root, filepath.Dir(c.symlink)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, c.symlink)); err != nil {
					t.Fatal(err)
				}
			}
			g, warnings := Run([]Repo{{Name: "app", Root: root}}, Options{Providers: tc})
			if g.Node(c.leaked) != nil {
				t.Errorf("%s exists: the file outside the repository was analysed", c.leaked)
			}
			if got := strings.Join(warnings, "\n"); !strings.Contains(got, "escapes") {
				t.Errorf("the refusal must be reported as a warning, got %q", got)
			}
		})
	}
}

// "no makefile was found" blamed a makefile that existed but was refused or unreadable.
func TestMakeWarningSaysWhyTheMakefileWasNotRead(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".teamcity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.mk"), []byte("all:\n\techo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := `object App : BuildType({ steps { script { scriptContent = "make -f ../outside.mk all\nmake -C nowhere" } } })`
	if err := os.WriteFile(filepath.Join(root, ".teamcity", "settings.kts"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	_, warnings := Run([]Repo{{Name: "app", Root: root}}, Options{Providers: tc})
	got := strings.Join(warnings, "\n")
	if !strings.Contains(got, `"." but its makefile was not read: path escapes from the repository`) {
		t.Errorf("a refused makefile must say why:\n%s", got)
	}
	if !strings.Contains(got, `"nowhere" but no makefile was found there`) {
		t.Errorf("a missing makefile keeps the old wording:\n%s", got)
	}
}
