package dsl

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Context is the data available while a label / probe expression is evaluated.
type Context struct {
	OIDs    map[string]any
	Columns map[string][]float64
	Rates   map[string]float64
	Vars    map[string]string
}

func NewContext() Context {
	return Context{OIDs: map[string]any{}, Columns: map[string][]float64{}, Rates: map[string]float64{}, Vars: map[string]string{}}
}

// Expression is a tiny, safe expression language compatible with the common
// function-style expressions used by The Dude: oid(), oid_column(), if(),
// concatenate(), string_size(), rate(), bitrate(), round(), etc.
type Expression struct {
	root node
}

type node interface {
	eval(*Context) (any, error)
}

func Parse(src string) (*Expression, error) {
	p := &parser{lex: lex(src)}
	tokens, err := p.lex.all()
	if err != nil {
		return nil, err
	}
	p.tokens = tokens
	if len(p.tokens) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().lit)
	}
	return &Expression{root: root}, nil
}

func (e *Expression) Eval(ctx *Context) (any, error) {
	return e.root.eval(ctx)
}

func EvalToString(src string, ctx *Context) string {
	expr, err := Parse(src)
	if err != nil {
		return ""
	}
	v, err := expr.Eval(ctx)
	if err != nil || v == nil {
		return ""
	}
	return Stringify(v)
}

func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return formatNumber(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []float64:
		parts := make([]string, 0, len(t))
		for _, n := range t {
			parts = append(parts, formatNumber(n))
		}
		return strings.Join(parts, ",")
	case []any:
		parts := make([]string, 0, len(t))
		for _, n := range t {
			parts = append(parts, Stringify(n))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func formatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "—"
	}
	if math.Abs(f-math.Round(f)) < 0.00001 {
		return strconv.FormatInt(int64(math.Round(f)), 10)
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, err
		}
		return n, nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to number", v)
	}
}

func toBool(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	case string:
		return t != "" && t != "0" && !strings.EqualFold(t, "false")
	default:
		return true
	}
}

func toStr(v any) string { return Stringify(v) }

// ─── Lexer ──────────────────────────────────────────────────────────────────

type tokenType int

const (
	tokEOF tokenType = iota
	tokNumber
	tokString
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	typ tokenType
	lit string
}

type lexer struct {
	src string
	pos int
}

func lex(src string) *lexer { return &lexer{src: src} }

func (l *lexer) all() ([]token, error) {
	var out []token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if t.typ == tokEOF {
			return out, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && unicode.IsSpace(rune(l.src[l.pos])) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{typ: tokEOF}, nil
	}
	ch := l.src[l.pos]
	if ch == '(' {
		l.pos++
		return token{typ: tokLParen, lit: "("}, nil
	}
	if ch == ')' {
		l.pos++
		return token{typ: tokRParen, lit: ")"}, nil
	}
	if ch == ',' {
		l.pos++
		return token{typ: tokComma, lit: ","}, nil
	}
	if ch == '"' || ch == '\'' {
		quote := ch
		l.pos++
		var b strings.Builder
		for l.pos < len(l.src) {
			c := l.src[l.pos]
			if c == '\\' && l.pos+1 < len(l.src) {
				l.pos++
				switch l.src[l.pos] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(l.src[l.pos])
				}
				l.pos++
				continue
			}
			if c == quote {
				l.pos++
				return token{typ: tokString, lit: b.String()}, nil
			}
			b.WriteByte(c)
			l.pos++
		}
		return token{}, fmt.Errorf("unterminated string")
	}
	if (ch >= '0' && ch <= '9') || (ch == '.' && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9') {
		start := l.pos
		for l.pos < len(l.src) && ((l.src[l.pos] >= '0' && l.src[l.pos] <= '9') || l.src[l.pos] == '.') {
			l.pos++
		}
		return token{typ: tokNumber, lit: l.src[start:l.pos]}, nil
	}
	if unicode.IsLetter(rune(ch)) || ch == '_' {
		start := l.pos
		for l.pos < len(l.src) {
			c := rune(l.src[l.pos])
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '.' && c != ':' {
				break
			}
			l.pos++
		}
		return token{typ: tokIdent, lit: l.src[start:l.pos]}, nil
	}
	for _, op := range []string{"==", "!=", ">=", "<=", "&&", "||"} {
		if strings.HasPrefix(l.src[l.pos:], op) {
			l.pos += len(op)
			return token{typ: tokOp, lit: op}, nil
		}
	}
	if strings.ContainsRune("+-*/%<>!()=", rune(ch)) {
		l.pos++
		return token{typ: tokOp, lit: string(ch)}, nil
	}
	return token{}, fmt.Errorf("unexpected character %q", ch)
}

// ─── Parser ─────────────────────────────────────────────────────────────────

type parser struct {
	lex    *lexer
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{typ: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) take() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) expect(tt tokenType, lit string) error {
	t := p.take()
	if t.typ != tt || (lit != "" && t.lit != lit) {
		return fmt.Errorf("expected %q, got %q", lit, t.lit)
	}
	return nil
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tokOp && (t.lit == "||" || strings.EqualFold(t.lit, "or")) {
			p.take()
			right, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			left = &binary{op: "||", left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tokOp && (t.lit == "&&" || strings.EqualFold(t.lit, "and")) {
			p.take()
			right, err := p.parseCompare()
			if err != nil {
				return nil, err
			}
			left = &binary{op: "&&", left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseCompare() (node, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tokOp && (t.lit == "==" || t.lit == "!=" || t.lit == ">" || t.lit == "<" || t.lit == ">=" || t.lit == "<=") {
			p.take()
			right, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			left = &binary{op: t.lit, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseAdd() (node, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tokOp && (t.lit == "+" || t.lit == "-") {
			p.take()
			right, err := p.parseMul()
			if err != nil {
				return nil, err
			}
			left = &binary{op: t.lit, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseMul() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tokOp && (t.lit == "*" || t.lit == "/" || t.lit == "%") {
			p.take()
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = &binary{op: t.lit, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseUnary() (node, error) {
	t := p.peek()
	if t.typ == tokOp && (t.lit == "-" || t.lit == "!") {
		p.take()
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unary{op: t.lit, child: child}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (node, error) {
	t := p.take()
	switch t.typ {
	case tokNumber:
		n, err := strconv.ParseFloat(t.lit, 64)
		if err != nil {
			return nil, err
		}
		return &literal{v: n}, nil
	case tokString:
		return &literal{v: t.lit}, nil
	case tokIdent:
		if p.peek().typ == tokLParen {
			p.take()
			var args []node
			if p.peek().typ != tokRParen {
				for {
					arg, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.peek().typ == tokComma {
						p.take()
						continue
					}
					break
				}
			}
			if err := p.expect(tokRParen, ")"); err != nil {
				return nil, err
			}
			return &call{name: t.lit, args: args}, nil
		}
		return &variable{name: t.lit}, nil
	case tokLParen:
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokRParen, ")"); err != nil {
			return nil, err
		}
		return inner, nil
	default:
		return nil, fmt.Errorf("unexpected token %q", t.lit)
	}
}

// ─── Evaluator ──────────────────────────────────────────────────────────────

type literal struct{ v any }

func (n *literal) eval(*Context) (any, error) { return n.v, nil }

type variable struct{ name string }

func (n *variable) eval(ctx *Context) (any, error) {
	if ctx == nil {
		return "", nil
	}
	if v, ok := ctx.Vars[n.name]; ok {
		return v, nil
	}
	if v, ok := ctx.OIDs[n.name]; ok {
		return v, nil
	}
	return "", nil
}

type unary struct {
	op    string
	child node
}

func (n *unary) eval(ctx *Context) (any, error) {
	v, err := n.child.eval(ctx)
	if err != nil {
		return nil, err
	}
	if n.op == "!" {
		return !toBool(v), nil
	}
	f, err := toFloat(v)
	if err != nil {
		return nil, err
	}
	return -f, nil
}

type binary struct {
	op          string
	left, right node
}

func (n *binary) eval(ctx *Context) (any, error) {
	lv, err := n.left.eval(ctx)
	if err != nil {
		return nil, err
	}
	if n.op == "&&" && !toBool(lv) {
		return false, nil
	}
	if n.op == "||" && toBool(lv) {
		return true, nil
	}
	rv, err := n.right.eval(ctx)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "&&":
		return toBool(lv) && toBool(rv), nil
	case "||":
		return toBool(lv) || toBool(rv), nil
	case "==":
		return toStr(lv) == toStr(rv), nil
	case "!=":
		return toStr(lv) != toStr(rv), nil
	case ">", "<", ">=", "<=":
		lf, le := toFloat(lv)
		rf, re := toFloat(rv)
		if le != nil || re != nil {
			return false, nil
		}
		switch n.op {
		case ">":
			return lf > rf, nil
		case "<":
			return lf < rf, nil
		case ">=":
			return lf >= rf, nil
		default:
			return lf <= rf, nil
		}
	case "+":
		if _, ok := lv.(string); ok {
			return toStr(lv) + toStr(rv), nil
		}
		lf, le := toFloat(lv)
		rf, re := toFloat(rv)
		if le != nil || re != nil {
			return toStr(lv) + toStr(rv), nil
		}
		return lf + rf, nil
	case "-", "*", "/", "%":
		lf, le := toFloat(lv)
		rf, re := toFloat(rv)
		if le != nil || re != nil {
			return nil, fmt.Errorf("arithmetic on non-numeric value")
		}
		switch n.op {
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		case "/":
			if rf == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return lf / rf, nil
		default:
			if rf == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return math.Mod(lf, rf), nil
		}
	}
	return nil, fmt.Errorf("unknown operator %s", n.op)
}

type call struct {
	name string
	args []node
}

func (n *call) eval(ctx *Context) (any, error) {
	args := make([]any, 0, len(n.args))
	// `if` is eagerly evaluated for simplicity; all common labels provide safe
	// defaults in both branches.
	for _, a := range n.args {
		v, err := a.eval(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	if ctx == nil {
		ctx = &Context{OIDs: map[string]any{}, Columns: map[string][]float64{}, Rates: map[string]float64{}}
	}
	name := strings.ToLower(n.name)
	switch name {
	case "if":
		if len(args) != 3 {
			return nil, fmt.Errorf("if expects 3 arguments")
		}
		if toBool(args[0]) {
			return args[1], nil
		}
		return args[2], nil
	case "oid":
		if len(args) == 0 {
			return "", nil
		}
		key := strings.TrimSpace(toStr(args[0]))
		if v, ok := ctx.OIDs[key]; ok {
			return v, nil
		}
		// Some devices return a table suffix; try a unique prefix match.
		for k, v := range ctx.OIDs {
			if strings.HasPrefix(k, key+".") {
				return v, nil
			}
		}
		return "", nil
	case "oid_column":
		if len(args) == 0 {
			return []float64{}, nil
		}
		key := strings.TrimSpace(toStr(args[0]))
		if v, ok := ctx.Columns[key]; ok {
			return v, nil
		}
		return []float64{}, nil
	case "rate":
		if len(args) == 0 {
			return 0.0, nil
		}
		return ctx.Rates[strings.TrimSpace(toStr(args[0]))], nil
	case "string_size":
		if len(args) == 0 {
			return 0, nil
		}
		return len(toStr(args[0])), nil
	case "concatenate", "concat":
		var b strings.Builder
		for _, a := range args {
			b.WriteString(toStr(a))
		}
		return b.String(), nil
	case "coalesce":
		for _, a := range args {
			if toStr(a) != "" {
				return a, nil
			}
		}
		return "", nil
	case "round":
		if len(args) == 0 {
			return 0.0, nil
		}
		f, err := toFloat(args[0])
		if err != nil {
			return nil, err
		}
		places := 0
		if len(args) > 1 {
			if p, err := toFloat(args[1]); err == nil {
				places = int(p)
			}
		}
		mult := math.Pow(10, float64(places))
		return math.Round(f*mult) / mult, nil
	case "avg":
		vals := flatten(args)
		if len(vals) == 0 {
			return 0.0, nil
		}
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum / float64(len(vals)), nil
	case "sum":
		vals := flatten(args)
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum, nil
	case "min":
		vals := flatten(args)
		if len(vals) == 0 {
			return 0.0, nil
		}
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		return m, nil
	case "max":
		vals := flatten(args)
		if len(vals) == 0 {
			return 0.0, nil
		}
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		return m, nil
	case "bitrate":
		if len(args) == 0 {
			return "", nil
		}
		f, err := toFloat(args[0])
		if err != nil {
			return "", nil
		}
		return FormatBitrate(f), nil
	case "uptime_format":
		if len(args) == 0 {
			return "", nil
		}
		f, err := toFloat(args[0])
		if err != nil {
			return "", nil
		}
		return FormatUptime(f), nil
	case "number", "float":
		if len(args) == 0 {
			return 0.0, nil
		}
		f, err := toFloat(args[0])
		if err != nil {
			return 0.0, nil
		}
		return f, nil
	case "string", "str":
		if len(args) == 0 {
			return "", nil
		}
		return toStr(args[0]), nil
	case "abs":
		if len(args) == 0 {
			return 0.0, nil
		}
		f, _ := toFloat(args[0])
		return math.Abs(f), nil
	}
	return nil, fmt.Errorf("unknown function %q", n.name)
}

func flatten(args []any) []float64 {
	var out []float64
	for _, a := range args {
		switch t := a.(type) {
		case []float64:
			out = append(out, t...)
		case []any:
			for _, x := range t {
				if f, err := toFloat(x); err == nil {
					out = append(out, f)
				}
			}
		default:
			if f, err := toFloat(a); err == nil {
				out = append(out, f)
			}
		}
	}
	return out
}

func FormatBitrate(bps float64) string {
	if bps < 0 {
		bps = 0
	}
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", bps, units[i])
	}
	return fmt.Sprintf("%.2f %s", bps, units[i])
}

func FormatUptime(ticks float64) string {
	// sysUpTime is TimeTicks (1/100s). If the value looks like seconds already
	// (small numbers), keep the fallback sane.
	seconds := int64(ticks / 100)
	if ticks > 0 && ticks < 10000000 {
		seconds = int64(ticks / 100)
	}
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	mins := seconds / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %02dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh %02dm", hours, mins)
}

// RenderLabel substitutes [Device.X], [Submap.X], [Link.X] and [function()]
// tokens in a map label template.
func RenderLabel(template string, ctx *Context, functions map[string]string) string {
	if template == "" {
		return ""
	}
	// Labels imported from SQL/JSON often contain a literal "\n"; normalise it
	// so map nodes render multi-line labels without leaking escape sequences.
	template = strings.ReplaceAll(template, `\n`, "\n")
	var out strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '[' {
			out.WriteByte(template[i])
			i++
			continue
		}
		end := strings.IndexByte(template[i+1:], ']')
		if end < 0 {
			out.WriteString(template[i:])
			break
		}
		token := strings.TrimSpace(template[i+1 : i+1+end])
		i += end + 2
		if strings.HasSuffix(token, "()") {
			name := strings.TrimSuffix(token, "()")
			name = strings.TrimPrefix(name, "f:")
			expr := name
			if code, ok := functions[name]; ok {
				expr = code
			}
			out.WriteString(EvalToString(expr, ctx))
			continue
		}
		if v, ok := ctx.Vars[token]; ok {
			out.WriteString(v)
			continue
		}
		// unknown token is left blank rather than breaking the whole label
	}
	return strings.TrimSpace(out.String())
}
