// Package shell finds interesting command invocations in shell script text:
// make calls, repo-local scripts, and docker image builds/pushes/pulls.
// It is a lexical scan, not a shell interpreter.
package shell

import (
	"path"
	"regexp"
	"strings"
)

type Make struct {
	Dir     string // -C, relative to the script's working directory
	File    string // -f
	Targets []string
	Vars    map[string]string
	Line    string
}

type Docker struct {
	Action     string // build, push, pull, run, tag
	Images     []string
	Consumed   []string // images used by a build (build-arg values, tag source)
	Dockerfile string   // build -f
	Context    string   // build context dir
	Line       string
}

type Script struct {
	Path string // relative to the working directory
	Line string
}

type Result struct {
	Makes   []Make
	Dockers []Docker
	Scripts []Script
}

// Extract scans script text. cwd changes via "cd dir" are tracked, relative to "".
func Extract(text string) Result {
	var res Result
	cwd := "."
	for _, line := range logicalLines(text) {
		for _, cmd := range splitCommands(line) {
			words := Words(cmd)
			words = stripPrefix(words)
			if len(words) == 0 {
				continue
			}
			switch name := words[0]; {
			case name == "cd" && len(words) > 1:
				if strings.HasPrefix(words[1], "/") || strings.ContainsAny(words[1], "$%~") {
					cwd = "."
				} else {
					cwd = path.Clean(path.Join(cwd, words[1]))
				}
			case isMake(name):
				mk := parseMake(words[1:])
				mk.Dir = path.Clean(path.Join(cwd, mk.Dir))
				mk.Line = cmd
				res.Makes = append(res.Makes, mk)
			case name == "docker" || name == "podman" || name == "buildah":
				if d, ok := parseDocker(words[1:]); ok {
					d.Line = cmd
					if d.Context != "" && !strings.HasPrefix(d.Context, "/") {
						d.Context = path.Clean(path.Join(cwd, d.Context))
					}
					if d.Dockerfile != "" && !strings.HasPrefix(d.Dockerfile, "/") {
						d.Dockerfile = path.Clean(path.Join(cwd, d.Dockerfile))
					}
					res.Dockers = append(res.Dockers, d)
				}
			default:
				if p := scriptPath(words); p != "" {
					res.Scripts = append(res.Scripts, Script{Path: path.Clean(path.Join(cwd, p)), Line: cmd})
				}
			}
		}
	}
	return res
}

func isMake(w string) bool {
	switch w {
	case "make", "gmake", "$(MAKE)", "${MAKE}", "$MAKE", "/usr/bin/make":
		return true
	}
	return false
}

// stripPrefix removes env assignments and wrapper commands (sudo, time, ...).
func stripPrefix(w []string) []string {
	for len(w) > 0 {
		switch {
		case w[0] == "sudo" || w[0] == "time" || w[0] == "exec" || w[0] == "command" || w[0] == "nice" || w[0] == "!" ||
			w[0] == "then" || w[0] == "else" || w[0] == "do": // `if x; then make y; fi`
			w = w[1:]
		case w[0] == "env":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
		case strings.HasPrefix(w[0], "@") || strings.HasPrefix(w[0], "-") && len(w[0]) > 1 && !strings.HasPrefix(w[0], "--"):
			// make recipe prefixes @ - +
			w[0] = strings.TrimLeft(w[0], "@-+")
			if w[0] == "" {
				w = w[1:]
			}
		case strings.HasPrefix(w[0], "+") && len(w[0]) > 1:
			w[0] = w[0][1:]
		case isAssignment(w[0]):
			w = w[1:]
		default:
			return w
		}
	}
	return w
}

func isAssignment(w string) bool {
	i := strings.Index(w, "=")
	if i <= 0 {
		return false
	}
	for _, c := range w[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func parseMake(args []string) Make {
	m := Make{Vars: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-C" || a == "--directory":
			m.Dir = path.Join(m.Dir, next())
		case strings.HasPrefix(a, "--directory="):
			m.Dir = path.Join(m.Dir, a[len("--directory="):])
		case strings.HasPrefix(a, "-C"):
			m.Dir = path.Join(m.Dir, a[2:])
		case a == "-f" || a == "--file" || a == "--makefile":
			m.File = next()
		case strings.HasPrefix(a, "--file=") || strings.HasPrefix(a, "--makefile="):
			m.File = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "-f") && len(a) > 2:
			m.File = a[2:]
		case a == "-I" || a == "-o" || a == "-W" || a == "--include-dir" || a == "--old-file" || a == "--what-if":
			next()
		case a == "-j" || a == "-l" || a == "--jobs" || a == "--load-average":
			if i+1 < len(args) && isNumber(args[i+1]) {
				i++
			}
		case strings.HasPrefix(a, "-"):
		case isAssignment(a):
			k, v, _ := strings.Cut(a, "=")
			m.Vars[k] = v
		default:
			m.Targets = append(m.Targets, a)
		}
	}
	return m
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// docker options that take a value (so the value is not mistaken for the image).
var dockerValueOpts = map[string]bool{
	"-t": true, "--tag": true, "-f": true, "--file": true, "--build-arg": true, "--target": true,
	"--platform": true, "-e": true, "--env": true, "-v": true, "--volume": true, "-p": true,
	"--publish": true, "--name": true, "-w": true, "--workdir": true, "--network": true,
	"--entrypoint": true, "-u": true, "--user": true, "--env-file": true, "--mount": true,
	"--label": true, "-l": true, "--cache-from": true, "--cache-to": true, "--secret": true,
	"--ssh": true, "--output": true, "-o": true, "--add-host": true, "--memory": true, "-m": true,
	"--cpus": true, "--restart": true, "--hostname": true, "-h": true, "--link": true,
	"--progress": true, "--build-context": true, "--shm-size": true, "--ulimit": true,
	"--network-alias": true, "--dns": true, "--device": true, "--gpus": true, "--pull": false,
}

func parseDocker(args []string) (Docker, bool) {
	// global options before the subcommand
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "-H" || args[0] == "--host" || args[0] == "--context" || args[0] == "--config" || args[0] == "-c" {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return Docker{}, false
	}
	sub := args[0]
	args = args[1:]
	switch sub {
	case "buildx", "image", "container":
		if len(args) == 0 {
			return Docker{}, false
		}
		sub, args = args[0], args[1:]
	}
	d := Docker{Action: sub}
	var pos []string
	push := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := ""
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
			a, val = k, v
		} else if dockerValueOpts[a] && i+1 < len(args) {
			i++
			val = args[i]
		} else if strings.HasPrefix(a, "-") {
			if a == "--push" {
				push = true
			}
			continue
		} else {
			pos = append(pos, a)
			if sub == "run" || sub == "create" {
				break // remaining words are the container command
			}
			continue
		}
		switch a {
		case "-t", "--tag":
			d.Images = append(d.Images, val)
		case "-f", "--file":
			d.Dockerfile = val
		case "--build-arg":
			if j := strings.Index(val, "="); j >= 0 && LooksLikeImage(val[j+1:]) {
				d.Consumed = append(d.Consumed, val[j+1:])
			}
		case "--cache-from":
			if LooksLikeImage(val) {
				d.Consumed = append(d.Consumed, val)
			}
		}
	}
	switch sub {
	case "build":
		if len(pos) > 0 {
			d.Context = pos[0]
		}
		if push {
			d.Action = "build+push"
		}
	case "push", "pull":
		if len(pos) == 0 {
			return d, false
		}
		d.Images = append(d.Images, pos[0])
	case "run", "create":
		if len(pos) == 0 {
			return d, false
		}
		d.Consumed = append(d.Consumed, pos[0])
	case "tag":
		if len(pos) < 2 {
			return d, false
		}
		d.Consumed = append(d.Consumed, pos[0])
		d.Images = append(d.Images, pos[1])
	default:
		return d, false
	}
	return d, true
}

// LooksLikeImage is a heuristic for registry/name[:tag] references.
func LooksLikeImage(s string) bool {
	s = strings.Trim(s, `"'`)
	if s == "" || strings.ContainsAny(s, " =") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") {
		return false
	}
	return strings.Contains(s, "/") && (strings.Contains(s, ":") || strings.Contains(s, "."))
}

// scriptPath recognises "./ci/x.sh", "bash ci/x.sh", "sh -e x.sh", "source x.sh".
func scriptPath(w []string) string {
	switch w[0] {
	case "bash", "sh", "zsh", "source", ".":
		for _, a := range w[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if isLocalPath(a) {
				return a
			}
			return ""
		}
		return ""
	}
	if strings.HasPrefix(w[0], "./") || (strings.Contains(w[0], "/") && strings.HasSuffix(w[0], ".sh") && !strings.HasPrefix(w[0], "/")) {
		if isLocalPath(w[0]) {
			return w[0]
		}
	}
	return ""
}

func isLocalPath(a string) bool {
	return !strings.HasPrefix(a, "/") && !strings.ContainsAny(a, "$%`*") && a != ""
}

// logicalLines joins backslash continuations and drops comments.
func logicalLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.HasSuffix(l, `\`) {
			cur.WriteString(strings.TrimSuffix(l, `\`))
			cur.WriteByte(' ')
			continue
		}
		cur.WriteString(l)
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out = append(out, s)
	}
	return out
}

// splitCommands splits on ; && || | & outside quotes and $( ).
func splitCommands(line string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(line) {
				cur.WriteByte(c)
				i++
				c = line[i]
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\' && i+1 < len(line):
			cur.WriteByte(c)
			i++
			c = line[i]
		case c == '$' && i+1 < len(line) && (line[i+1] == '(' || line[i+1] == '{'):
			depth++
			cur.WriteByte(c)
			i++
			c = line[i]
		case (c == ')' || c == '}') && depth > 0:
			depth--
		case depth == 0 && (c == ';' || c == '|' || c == '&'):
			flush()
			continue
		case depth == 0 && c == '#' && (i == 0 || line[i-1] == ' '):
			flush()
			return out
		case depth == 0 && (c == '(' || c == ')' || c == '{' || c == '}') && (i == 0 || line[i-1] == ' ' || i+1 == len(line) || line[i+1] == ' '):
			// subshell / group delimiters
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

// Words splits a simple command into words, removing quotes.
func Words(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	inWord := false
	depth := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			if c == '\\' && quote == '"' && i+1 < len(cmd) {
				i++
				c = cmd[i]
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
			continue
		case c == '\\' && i+1 < len(cmd):
			i++
			c = cmd[i]
		case c == '$' && i+1 < len(cmd) && (cmd[i+1] == '(' || cmd[i+1] == '{'):
			depth++
			cur.WriteByte(c)
			i++
			c = cmd[i]
		case (c == ')' || c == '}') && depth > 0:
			depth--
		case (c == ' ' || c == '\t') && depth == 0:
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
			continue
		case (c == '>' || c == '<') && depth == 0:
			// redirection: drop the operator and its target
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
			for i+1 < len(cmd) && (cmd[i+1] == '>' || cmd[i+1] == '&' || cmd[i+1] == ' ') {
				i++
			}
			for i+1 < len(cmd) && cmd[i+1] != ' ' {
				i++
			}
			continue
		}
		cur.WriteByte(c)
		inWord = true
	}
	if inWord {
		out = append(out, cur.String())
	}
	// "2>&1" leftovers like a bare "2"
	return out
}

// DockerfileFrom returns base images referenced by FROM lines (with ARG defaults applied),
// skipping references to earlier build stages.
func DockerfileFrom(content string, buildArgs map[string]string) []string {
	args := map[string]string{}
	for k, v := range buildArgs {
		args[k] = v
	}
	stages := map[string]bool{}
	var out []string
	for _, l := range logicalLines(content) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "ARG":
			kv := f[1]
			if i := strings.Index(kv, "="); i >= 0 {
				if _, set := args[kv[:i]]; !set {
					args[kv[:i]] = strings.Trim(kv[i+1:], `"'`)
				}
			}
		case "FROM":
			img := ""
			for i := 1; i < len(f); i++ {
				if strings.HasPrefix(f[i], "--") {
					continue
				}
				img = f[i]
				if i+2 < len(f) && strings.EqualFold(f[i+1], "AS") {
					stages[strings.ToLower(f[i+2])] = true
				}
				break
			}
			img = expandArgs(img, args)
			if img != "" && !stages[strings.ToLower(img)] && img != "scratch" {
				out = append(out, img)
			}
		}
	}
	return out
}

var reArgRef = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)

// expandArgs replaces $NAME and ${NAME} by the value of that ARG, in one pass over the
// whole names: "$AB" is the argument AB, whether or not an argument A exists, and a value
// that itself contains "$A" is left as it is. Unknown names stay unexpanded.
func expandArgs(s string, args map[string]string) string {
	return reArgRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := ref[1:]
		if strings.HasPrefix(name, "{") {
			name = name[1 : len(name)-1]
		}
		if v, ok := args[name]; ok {
			return v
		}
		return ref
	})
}
