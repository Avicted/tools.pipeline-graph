// Package makefile statically parses GNU Makefiles: variables, rules,
// prerequisites, recipes and includes. Nothing is ever executed; $(shell ...)
// and other functions are left unexpanded.
package makefile

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Rule struct {
	Target  string
	Prereqs []string
	Recipe  []string
	File    string // path relative to the repo root
	Line    int
}

type Makefile struct {
	Path        string // relative to the repo root
	Dir         string // directory make runs in, relative to the repo root
	Rules       map[string]*Rule
	Vars        map[string]string
	Phony       map[string]bool
	DefaultGoal string
	Warnings    []string
}

var (
	reAssign = regexp.MustCompile(`^(?:(?:export|override|private)\s+)*([A-Za-z0-9_.\-]+)\s*(=|:=|::=|:::=|\?=|\+=|!=)\s*(.*)$`)
)

// Load parses the makefile at root/dir/file (file defaults to GNUmakefile|makefile|Makefile).
// cmdVars are variables given on the make command line; they override assignments.
func Load(root, dir, file string, cmdVars map[string]string) (*Makefile, error) {
	m := &Makefile{Dir: filepath.ToSlash(filepath.Clean(dir)), Rules: map[string]*Rule{}, Vars: map[string]string{}, Phony: map[string]bool{}}
	for k, v := range cmdVars {
		m.Vars[k] = v
	}
	if file == "" {
		for _, f := range []string{"GNUmakefile", "makefile", "Makefile"} {
			if _, err := os.Stat(filepath.Join(root, dir, f)); err == nil {
				file = f
				break
			}
		}
		if file == "" {
			return nil, os.ErrNotExist
		}
	}
	m.Path = filepath.ToSlash(filepath.Clean(filepath.Join(dir, file)))
	if err := m.parseFile(root, m.Path, cmdVars, 0); err != nil {
		return nil, err
	}
	if g, ok := m.Vars[".DEFAULT_GOAL"]; ok && g != "" {
		m.DefaultGoal = m.Expand(g)
	}
	return m, nil
}

func (m *Makefile) parseFile(root, rel string, cmdVars map[string]string, depth int) error {
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return err
	}
	defer f.Close()

	var lines []string
	var lineNo []int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		l := sc.Text()
		if len(lines) > 0 && strings.HasSuffix(lines[len(lines)-1], `\`) {
			prev := lines[len(lines)-1]
			lines[len(lines)-1] = strings.TrimRight(prev[:len(prev)-1], " \t") + " " + strings.TrimLeft(l, " \t")
			continue
		}
		lines = append(lines, l)
		lineNo = append(lineNo, n)
	}

	var cur []*Rule // rules receiving recipe lines
	inDefine := ""
	var defineBody []string
	for i, l := range lines {
		if inDefine != "" {
			if strings.TrimSpace(l) == "endef" {
				m.setVar(inDefine, "=", strings.Join(defineBody, "\n"), cmdVars)
				inDefine, defineBody = "", nil
			} else {
				defineBody = append(defineBody, l)
			}
			continue
		}
		if strings.HasPrefix(l, "\t") {
			for _, r := range cur {
				r.Recipe = append(r.Recipe, strings.TrimPrefix(l, "\t"))
			}
			continue
		}
		t := strings.TrimSpace(stripComment(l))
		if t == "" {
			continue
		}
		word := strings.Fields(t)[0]
		switch word {
		case "ifeq", "ifneq", "ifdef", "ifndef", "else", "endif":
			// conditionals are not evaluated: every branch is taken
			cur = nil
			continue
		case "define":
			inDefine = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t[len("define"):]), "="))
			continue
		case "include", "-include", "sinclude":
			for _, inc := range strings.Fields(m.Expand(t[len(word):])) {
				if strings.ContainsAny(inc, "$*?[") {
					m.Warnings = append(m.Warnings, rel+": include "+inc+" not resolved")
					continue
				}
				p := filepath.ToSlash(filepath.Clean(filepath.Join(m.Dir, inc)))
				if depth > 10 {
					continue
				}
				if err := m.parseFile(root, p, cmdVars, depth+1); err != nil && word == "include" {
					m.Warnings = append(m.Warnings, rel+": include "+inc+": not found")
				}
			}
			cur = nil
			continue
		case "export", "unexport", "vpath", "undefine":
			if !reAssign.MatchString(t) {
				continue
			}
		}
		if mm := reAssign.FindStringSubmatch(t); mm != nil && !isRuleLine(t) {
			m.setVar(mm[1], mm[2], mm[3], cmdVars)
			cur = nil
			continue
		}
		cur = m.parseRule(t, rel, lineNo[i])
	}
	return nil
}

// isRuleLine reports whether a line containing '=' is really a rule with a
// target-specific variable (e.g. "foo: CFLAGS = -O2").
func isRuleLine(t string) bool {
	c := strings.Index(t, ":")
	e := strings.Index(t, "=")
	if c < 0 || c > e {
		return false
	}
	// ":=" / "::=" are assignments
	return !strings.HasPrefix(t[c:], ":=") && !strings.HasPrefix(t[c:], "::=") && !strings.HasPrefix(t[c:], ":::=")
}

func (m *Makefile) setVar(name, op, val string, cmdVars map[string]string) {
	if _, ok := cmdVars[name]; ok {
		return
	}
	switch op {
	case "?=":
		if _, ok := m.Vars[name]; !ok {
			m.Vars[name] = val
		}
	case "+=":
		if old, ok := m.Vars[name]; ok && old != "" {
			m.Vars[name] = old + " " + val
		} else {
			m.Vars[name] = val
		}
	case ":=", "::=", ":::=":
		m.Vars[name] = m.Expand(val)
	case "!=":
		m.Vars[name] = "$(shell " + val + ")"
	default:
		m.Vars[name] = val
	}
}

func (m *Makefile) parseRule(t, file string, line int) []*Rule {
	// split "targets : prereqs ; recipe" (ignoring ':' inside $(...))
	colon := findColon(t)
	if colon < 0 {
		return nil
	}
	targetsPart := t[:colon]
	rest := strings.TrimLeft(t[colon+1:], ":") // double-colon rules
	var inline string
	if i := strings.Index(rest, ";"); i >= 0 {
		rest, inline = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	// target-specific variable: "target: VAR = value"
	if reAssign.MatchString(strings.TrimSpace(rest)) {
		return nil
	}
	// static pattern rule: "targets: target-pattern: prereq-patterns"
	if c := findColon(rest); c >= 0 {
		rest = rest[c+1:]
	}
	rest = strings.ReplaceAll(rest, "|", " ")
	targets := strings.Fields(m.Expand(targetsPart))
	prereqs := strings.Fields(m.Expand(rest))
	var out []*Rule
	for _, tg := range targets {
		if tg == ".PHONY" {
			for _, p := range prereqs {
				m.Phony[p] = true
			}
			continue
		}
		if strings.HasPrefix(tg, ".") && !strings.Contains(tg, "/") {
			continue // special targets
		}
		r := m.Rules[tg]
		if r == nil {
			r = &Rule{Target: tg, File: file, Line: line}
			m.Rules[tg] = r
			if m.DefaultGoal == "" && !strings.Contains(tg, "%") {
				m.DefaultGoal = tg
			}
		}
		r.Prereqs = appendUnique(r.Prereqs, prereqs...)
		if inline != "" {
			r.Recipe = append(r.Recipe, inline)
		}
		out = append(out, r)
	}
	return out
}

func findColon(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		case ':':
			if depth == 0 && !(i+1 < len(s) && s[i+1] == '=') {
				// Windows drive letters are not supported (GNU make on Linux)
				return i
			}
		}
	}
	return -1
}

func stripComment(l string) string {
	for i := 0; i < len(l); i++ {
		if l[i] == '\\' {
			i++
			continue
		}
		if l[i] == '#' {
			return l[:i]
		}
	}
	return l
}

func appendUnique(list []string, items ...string) []string {
	for _, it := range items {
		dup := false
		for _, x := range list {
			if x == it {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, it)
		}
	}
	return list
}

// Expand substitutes variable references. Unknown variables expand to "" like make
// does, except automatic variables and function calls which are kept verbatim.
func (m *Makefile) Expand(s string) string { return m.expand(s, 0) }

func (m *Makefile) expand(s string, depth int) string {
	if depth > 16 || !strings.Contains(s, "$") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		c := s[i+1]
		if c == '$' {
			b.WriteString("$$")
			i++
			continue
		}
		if c == '(' || c == '{' {
			end := matchParen(s, i+1)
			if end < 0 {
				b.WriteString(s[i:])
				break
			}
			inner := s[i+2 : end]
			if name := strings.TrimSpace(inner); reVarName.MatchString(name) {
				if v, ok := m.Vars[name]; ok {
					b.WriteString(m.expand(v, depth+1))
				} else if v, ok := builtinVars[name]; ok {
					b.WriteString(v)
				}
			} else {
				b.WriteString(s[i : end+1]) // function call: keep verbatim
			}
			i = end
			continue
		}
		// single-char variable ($@, $<, $X...)
		if v, ok := m.Vars[string(c)]; ok {
			b.WriteString(m.expand(v, depth+1))
		} else {
			b.WriteString(s[i : i+2])
		}
		i++
	}
	return b.String()
}

var (
	reVarName   = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	builtinVars = map[string]string{"MAKE": "$(MAKE)", "CURDIR": "$(CURDIR)", "MAKEFLAGS": "", "MAKEFILE_LIST": "$(MAKEFILE_LIST)"}
)

func matchParen(s string, open int) int {
	o := s[open]
	cl := byte(')')
	if o == '{' {
		cl = '}'
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case o:
			depth++
		case cl:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// Targets returns all explicit targets in sorted order.
func (m *Makefile) Targets() []string {
	out := make([]string, 0, len(m.Rules))
	for t := range m.Rules {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ExpandRecipe expands a recipe line in the context of a rule (automatic variables included).
func (m *Makefile) ExpandRecipe(r *Rule, line string) string {
	auto := map[string]string{"@": r.Target}
	if len(r.Prereqs) > 0 {
		auto["<"] = r.Prereqs[0]
		auto["^"] = strings.Join(r.Prereqs, " ")
	}
	saved := map[string]*string{}
	for k, v := range auto {
		if old, ok := m.Vars[k]; ok {
			o := old
			saved[k] = &o
		} else {
			saved[k] = nil
		}
		m.Vars[k] = v
	}
	out := m.Expand(line)
	for k, v := range saved {
		if v == nil {
			delete(m.Vars, k)
		} else {
			m.Vars[k] = *v
		}
	}
	return strings.ReplaceAll(out, "$$", "$")
}
