package kts

import "strings"

type NodeKind int

const (
	NStr    NodeKind = iota // Str
	NNum                    // Str
	NRef                    // Name
	NMember                 // Recv.Name
	NCall                   // Recv(Args) {Body}
	NLambda                 // { Body }
	NAssign                 // Name(target path) Op= Value
	NObject                 // object/class Name : Supers { Body }
	NFun                    // fun Name(Params) = Value | { Body }
	NVal                    // val/var Name = Value
	NBinary                 // Value Op Right
	NBlock                  // if/for/when/try/init etc: Body holds all nested statements
	NRaw                    // anything else
)

// Node is a deliberately loose Kotlin syntax tree: just enough structure to
// recognise DSL calls, assignments and declarations.
type Node struct {
	Kind   NodeKind
	Name   string
	Op     string
	Str    string
	Recv   *Node
	Args   []*Node // call args; named args are NAssign
	Params []string
	Supers []*Node
	Body   []*Node
	Value  *Node
	Right  *Node
	Line   int
}

// Path returns the dotted name of a Ref/Member chain ("a.b.c"), or "" otherwise.
func (n *Node) Path() string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case NRef:
		return n.Name
	case NMember:
		if p := n.Recv.Path(); p != "" {
			return p + "." + n.Name
		}
		return n.Name
	}
	return ""
}

// CallName returns the dotted name of the callee for NCall ("" otherwise).
func (n *Node) CallName() string {
	if n == nil || n.Kind != NCall {
		return ""
	}
	return n.Recv.Path()
}

// LastName is the final segment of CallName/Path.
func LastName(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Lambda returns the trailing lambda of a call or the first lambda argument.
func (n *Node) Lambda() *Node {
	if n == nil {
		return nil
	}
	if n.Kind == NLambda {
		return n
	}
	if n.Kind != NCall {
		return nil
	}
	if n.Value != nil {
		return n.Value
	}
	for _, a := range n.Args {
		if a.Kind == NLambda {
			return a
		}
	}
	return nil
}

func Parse(src string) []*Node {
	p := &parser{toks: Lex(src)}
	return p.statements("")
}

type parser struct {
	toks []Token
	pos  int
}

func (p *parser) cur() Token { return p.toks[p.pos] }
func (p *parser) next() Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}
func (p *parser) is(v string) bool {
	t := p.cur()
	return (t.Kind == TPunct || t.Kind == TIdent) && t.Val == v
}
func (p *parser) eof() bool { return p.cur().Kind == TEOF }

// statements parses until the closing token (or EOF).
func (p *parser) statements(close string) []*Node {
	var out []*Node
	for !p.eof() {
		if close != "" && p.is(close) {
			p.next()
			return out
		}
		if p.is(";") || p.is(",") {
			p.next()
			continue
		}
		if close == "" && (p.is("}") || p.is(")")) { // stray closer at top level
			p.next()
			continue
		}
		if close != "" && (p.is("}") || p.is(")") || p.is("]")) { // mismatched closer: bail out
			return out
		}
		start := p.pos
		if n := p.statement(); n != nil {
			out = append(out, n)
		}
		if p.pos == start {
			p.next()
		}
	}
	return out
}

var modifiers = map[string]bool{
	"private": true, "public": true, "internal": true, "protected": true, "open": true,
	"abstract": true, "override": true, "const": true, "data": true, "sealed": true,
	"inline": true, "lateinit": true, "companion": true, "enum": true, "suspend": true,
	"final": true, "inner": true, "operator": true, "infix": true,
}

func (p *parser) statement() *Node {
	t := p.cur()
	if t.Kind == TPunct && t.Val == "@" { // annotation
		p.next()
		p.next()
		if p.is("(") {
			p.skipGroup()
		}
		return nil
	}
	if t.Kind == TIdent {
		switch {
		case t.Val == "import" || t.Val == "package":
			p.next()
			for !p.eof() && !p.cur().NL {
				p.next()
			}
			return nil
		case modifiers[t.Val] && p.toks[p.pos+1].Kind == TIdent:
			p.next()
			return p.statement()
		case t.Val == "object" || t.Val == "class" || t.Val == "interface":
			return p.objectDecl()
		case t.Val == "fun":
			return p.funDecl()
		case t.Val == "val" || t.Val == "var":
			return p.valDecl()
		case t.Val == "return" || t.Val == "throw":
			p.next()
			if p.cur().NL || p.is("}") {
				return nil
			}
			return p.expr()
		}
	}
	e := p.expr()
	if e == nil {
		return nil
	}
	if p.cur().Kind == TPunct {
		switch op := p.cur().Val; op {
		case "=", "+=", "-=":
			p.next()
			return &Node{Kind: NAssign, Name: e.Path(), Op: op, Recv: e, Value: p.expr(), Line: t.Line}
		}
	}
	return e
}

func (p *parser) objectDecl() *Node {
	t := p.next()
	n := &Node{Kind: NObject, Op: t.Val, Line: t.Line} // Op: object, class or interface
	if p.cur().Kind == TIdent && !p.is("object") {
		n.Name = p.next().Val
	}
	if p.is("<") {
		p.skipAngles()
	}
	for p.cur().Kind == TIdent && (modifiers[p.cur().Val] || p.is("constructor")) {
		p.next()
	}
	if p.is("(") { // primary constructor
		n.Params = p.params()
	}
	if p.is(":") {
		p.next()
		for {
			s := p.postfix(false)
			if s == nil {
				break
			}
			n.Supers = append(n.Supers, s)
			if !p.is(",") {
				break
			}
			p.next()
		}
	}
	if p.is("{") {
		p.next()
		n.Body = p.statements("}")
	}
	return n
}

func (p *parser) funDecl() *Node {
	line := p.next().Line
	n := &Node{Kind: NFun, Line: line}
	if p.is("<") {
		p.skipAngles()
	}
	// receiver type: fun Foo.bar(...)
	for p.cur().Kind == TIdent {
		n.Name = p.next().Val
		if p.is(".") {
			p.next()
			continue
		}
		break
	}
	if p.is("(") {
		n.Params = p.params()
	}
	if p.is(":") { // return type
		p.next()
		p.typeRef()
	}
	switch {
	case p.is("="):
		p.next()
		n.Value = p.expr()
	case p.is("{"):
		p.next()
		n.Body = p.statements("}")
	}
	return n
}

// params reads a parameter list "(a: T, val b: U = x)" and returns the names.
func (p *parser) params() []string {
	var out []string
	p.next()
	depth := 1
	expectName := true
	for !p.eof() && depth > 0 {
		t := p.next()
		switch {
		case t.Kind == TPunct && (t.Val == "(" || t.Val == "<" || t.Val == "{"):
			depth++
		case t.Kind == TPunct && (t.Val == ")" || t.Val == ">" || t.Val == "}"):
			depth--
		case t.Kind == TPunct && t.Val == "," && depth == 1:
			expectName = true
		case t.Kind == TIdent && expectName && depth == 1 && !modifiers[t.Val] && t.Val != "vararg" && t.Val != "val" && t.Val != "var":
			out = append(out, t.Val)
			expectName = false
		}
	}
	return out
}

func (p *parser) valDecl() *Node {
	line := p.next().Line
	n := &Node{Kind: NVal, Line: line}
	if p.is("(") { // destructuring
		p.skipGroup()
	} else if p.cur().Kind == TIdent {
		n.Name = p.next().Val
		for p.is(".") { // extension property
			p.next()
			n.Name = p.next().Val
		}
	}
	if p.is(":") {
		p.next()
		p.typeRef()
	}
	if p.is("=") {
		p.next()
		n.Value = p.expr()
	} else if p.is("by") {
		p.next()
		n.Value = p.expr()
	}
	return n
}

func (p *parser) typeRef() {
	for p.cur().Kind == TIdent || p.is(".") || p.is("?") {
		p.next()
		if p.is("<") {
			p.skipAngles()
		}
		if p.is("(") { // function type
			p.skipGroup()
			if p.is("->") {
				p.next()
				continue
			}
		}
	}
}

func (p *parser) skipGroup() {
	open := p.next().Val
	close := map[string]string{"(": ")", "{": "}", "[": "]"}[open]
	depth := 1
	for !p.eof() && depth > 0 {
		t := p.next()
		if t.Kind == TPunct && t.Val == open {
			depth++
		} else if t.Kind == TPunct && t.Val == close {
			depth--
		}
	}
}

func (p *parser) skipAngles() {
	depth := 0
	for !p.eof() {
		t := p.next()
		if t.Val == "<" {
			depth++
		} else if t.Val == ">" {
			depth--
			if depth == 0 {
				return
			}
		} else if t.Kind == TPunct && (t.Val == "{" || t.Val == "}" || t.Val == ";") {
			return
		}
	}
}

var binaryOps = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "%": true, "?:": true, "&&": true, "||": true,
	"==": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true, "===": true, "!==": true,
	"..": true, "..<": true, "in": true, "is": true, "as": true, "to": true, "until": true,
	"downTo": true, "step": true, "!in": true,
}

func (p *parser) expr() *Node {
	left := p.unary()
	if left == nil {
		return nil
	}
	for {
		t := p.cur()
		if (t.Kind != TPunct && t.Kind != TIdent) || !binaryOps[t.Val] {
			return left
		}
		// an identifier operator on a new line starts a new statement
		if t.Kind == TIdent && t.NL {
			return left
		}
		if t.Val == "as" || t.Val == "is" {
			p.next()
			if p.is("?") {
				p.next()
			}
			p.typeRef()
			continue
		}
		p.next()
		right := p.unary()
		left = &Node{Kind: NBinary, Op: t.Val, Value: left, Right: right, Line: t.Line}
	}
}

func (p *parser) unary() *Node {
	if p.is("!") || p.is("-") || p.is("+") || p.is("++") || p.is("--") {
		p.next()
	}
	return p.postfix(true)
}

// postfix parses a primary followed by member access, calls and trailing lambdas.
func (p *parser) postfix(allowLambda bool) *Node {
	n := p.primary()
	if n == nil {
		return nil
	}
	for {
		t := p.cur()
		switch {
		case t.Kind == TPunct && (t.Val == "." || t.Val == "?." || t.Val == "::"):
			p.next()
			if p.cur().Kind == TIdent {
				n = &Node{Kind: NMember, Recv: n, Name: p.next().Val, Line: t.Line}
			} else if p.is("class") {
				p.next()
			}
		case t.Kind == TPunct && t.Val == "(" && !t.NL:
			p.next()
			call := &Node{Kind: NCall, Recv: n, Line: t.Line}
			call.Args = p.args()
			n = call
		case t.Kind == TPunct && t.Val == "<" && !t.NL && p.looksLikeTypeArgs():
			p.skipAngles()
		case t.Kind == TPunct && t.Val == "{" && !t.NL && allowLambda:
			lam := p.lambda()
			if n.Kind == NCall && n.Value == nil {
				n.Value = lam
			} else {
				n = &Node{Kind: NCall, Recv: n, Value: lam, Line: t.Line}
			}
		case t.Kind == TPunct && (t.Val == "!!" || t.Val == "++" || t.Val == "--"):
			p.next()
		case t.Kind == TPunct && t.Val == "[" && !t.NL:
			p.skipGroup()
		default:
			return n
		}
	}
}

// looksLikeTypeArgs distinguishes listOf<String>() from a < b.
func (p *parser) looksLikeTypeArgs() bool {
	depth := 0
	for i := p.pos; i < len(p.toks) && i < p.pos+32; i++ {
		t := p.toks[i]
		switch {
		case t.Val == "<":
			depth++
		case t.Val == ">":
			depth--
			if depth == 0 {
				nx := p.toks[i+1]
				return nx.Val == "(" || nx.Val == "{" || nx.Val == "." || nx.Val == "::"
			}
		case t.Kind == TIdent || t.Val == "," || t.Val == "." || t.Val == "?" || t.Val == "*":
		default:
			return false
		}
	}
	return false
}

func (p *parser) args() []*Node {
	var out []*Node
	for !p.eof() {
		if p.is(")") {
			p.next()
			return out
		}
		if p.is(",") {
			p.next()
			continue
		}
		if p.is("}") || p.is("]") { // malformed
			return out
		}
		start := p.pos
		// named argument
		if p.cur().Kind == TIdent && p.toks[p.pos+1].Val == "=" {
			name := p.next().Val
			p.next()
			out = append(out, &Node{Kind: NAssign, Name: name, Op: "=", Value: p.expr(), Line: p.cur().Line})
		} else if p.is("*") { // spread
			p.next()
			continue
		} else if e := p.expr(); e != nil {
			out = append(out, e)
		}
		if p.pos == start {
			p.next()
		}
	}
	return out
}

func (p *parser) lambda() *Node {
	line := p.next().Line // {
	n := &Node{Kind: NLambda, Line: line}
	// optional parameters: { a, b -> ... }
	for i := p.pos; i < len(p.toks) && i < p.pos+16; i++ {
		t := p.toks[i]
		if t.Val == "->" {
			for p.pos <= i {
				if t := p.next(); t.Kind == TIdent {
					n.Params = append(n.Params, t.Val)
				}
			}
			break
		}
		if !(t.Kind == TIdent || t.Val == "," || t.Val == ":" || t.Val == "(" || t.Val == ")" || t.Val == "_") {
			break
		}
	}
	n.Body = p.statements("}")
	return n
}

func (p *parser) primary() *Node {
	t := p.cur()
	switch t.Kind {
	case TString:
		p.next()
		return &Node{Kind: NStr, Str: t.Val, Line: t.Line}
	case TNumber, TChar:
		p.next()
		return &Node{Kind: NNum, Str: t.Val, Line: t.Line}
	case TIdent:
		switch t.Val {
		case "if", "while", "for", "when", "try", "catch", "finally", "else", "do", "init":
			return p.control()
		case "object":
			return p.objectDecl()
		case "fun":
			return p.funDecl()
		}
		p.next()
		return &Node{Kind: NRef, Name: t.Val, Line: t.Line}
	case TPunct:
		switch t.Val {
		case "(":
			p.next()
			inner := p.args() // parenthesised expression (or tuple-ish)
			if len(inner) == 1 {
				return inner[0]
			}
			return &Node{Kind: NRaw, Body: inner, Line: t.Line}
		case "{":
			return p.lambda()
		case "[":
			p.skipGroup()
			return &Node{Kind: NRaw, Line: t.Line}
		case "@":
			p.next()
			p.next()
			return p.primary()
		}
	}
	return nil
}

// control parses if/for/when/try/... into a block that keeps every nested
// statement, so DSL calls inside conditionals are still discovered.
func (p *parser) control() *Node {
	t := p.next()
	n := &Node{Kind: NBlock, Name: t.Val, Line: t.Line}
	if p.is("(") {
		p.next()
		n.Args = p.args()
	}
	switch {
	case p.is("{"):
		p.next()
		n.Body = p.statements("}")
	case t.Val != "try" && t.Val != "finally" && t.Val != "init":
		if s := p.statement(); s != nil {
			n.Body = []*Node{s}
		}
	}
	// chained else / catch / finally
	if p.is("else") || p.is("catch") || p.is("finally") {
		n.Right = p.control()
	}
	return n
}
