package shell

import (
	"strings"
	"unicode/utf8"
)

// Truncate shortens s to at most max bytes. When it cuts it ends in "...", and it never cuts
// inside a character: repository text is not ASCII, and a half character is invalid UTF-8
// in everything that is written from it.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("...")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// Summary is the first line of a script that does something, shortened to 60 bytes: blank
// lines, comments and `set` options are skipped. It is "" for a script that does nothing.
func Summary(script string) string {
	for _, l := range strings.Split(script, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "set -") {
			continue
		}
		return Truncate(l, 60)
	}
	return ""
}

// BuildCommand spells a docker build as the shell command Extract reads back, for the CI
// systems whose docker steps are not shell: the analyzer then finds images, Dockerfiles and
// base images the same way as in scripts. An empty context is the current directory.
func BuildCommand(dockerfile string, tags, buildArgs []string, context string) string {
	cmd := "docker build"
	if dockerfile != "" {
		cmd += " -f " + dockerfile
	}
	for _, t := range tags {
		cmd += " -t " + t
	}
	for _, a := range buildArgs {
		cmd += " --build-arg " + a
	}
	if context == "" {
		context = "."
	}
	return cmd + " " + context
}

// PushCommands is one `docker push` per tag.
func PushCommands(tags []string) []string {
	var out []string
	for _, t := range tags {
		out = append(out, "docker push "+t)
	}
	return out
}
