// ============================================================
//
//	OFileDiff — Text · Binary · Directory Diff & Unified Patch
//	Author  : khaled.s.haddad
//	Website : owlsec.org
//	Language: Go (stdlib only) — OWL suite
//
//	Linear-space Myers diff with GNU-compatible unified output (including
//	"\ No newline at end of file"), whitespace / case / blank-line
//	options, word-level highlighting, side-by-side view, diffstat,
//	JSON and self-contained HTML reports, binary diff (prefix/suffix
//	alignment + patched byte ranges), recursive directory compare with a
//	-ruN style patch, and a built-in patch applier (offset search,
//	-p N, reverse, dry run, path-traversal guard).
//
// ============================================================
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---- OWL UI KIT (cyan "owl" house style) — stdlib only. Shared across the suite ----
// CYAN is the primary brand/frame colour; red is reserved for errors and
// removed lines.
const (
	R     = "\033[91m"       // red     — errors / removed
	GRN   = "\033[92m"       // green   — success / added
	YLW   = "\033[93m"       // yellow  — prompts / warning text
	CY    = "\033[96m"       // cyan    — primary brand & frames
	W     = "\033[97m"       // white   — body text
	B     = "\033[1m"        // bold
	X     = "\033[0m"        // reset
	MG    = "\033[95m"       // magenta — suite name / accents
	DIM   = "\033[2m"        // dim     — secondary text
	OR    = "\033[38;5;208m" // orange  — warning markers
	GY    = "\033[90m"       // gray    — muted
	DELHL = "\033[1;97;41m"  // changed word inside a removed line
	INSHL = "\033[1;30;42m"  // changed word inside an added line
)

var noColor = os.Getenv("NO_COLOR") != "" || !isatty()

func isatty() bool { fi, _ := os.Stdout.Stat(); return (fi.Mode() & os.ModeCharDevice) != 0 }
func col(s string, c ...string) string {
	if noColor {
		return s
	}
	return strings.Join(c, "") + s + X
}
func clear()           { fmt.Print("\033[H\033[2J") }
func rule()            { fmt.Println(col(strings.Repeat("─", 66), CY)) }
func okMsg(s string)   { fmt.Println("  " + col("[✔] ", GRN+B) + col(s, W)) }
func errMsg(s string)  { fmt.Println("  " + col("[✘] ", R+B) + col(s, R)) }
func warnMsg(s string) { fmt.Println("  " + col("[!] ", OR+B) + col(s, YLW)) }
func info(s string)    { fmt.Println("  " + col("[*] ", CY) + col(s, DIM+W)) }
func kv(k, v string)   { fmt.Println("  " + col("│  ", CY) + col(pad(k+":", 16), DIM) + col(v, W)) }

const BANNER = `
 ██████╗ ███████╗██╗██╗     ███████╗██████╗ ██╗███████╗███████╗
██╔═══██╗██╔════╝██║██║     ██╔════╝██╔══██╗██║██╔════╝██╔════╝
██║   ██║█████╗  ██║██║     █████╗  ██║  ██║██║█████╗  █████╗
██║   ██║██╔══╝  ██║██║     ██╔══╝  ██║  ██║██║██╔══╝  ██╔══╝
╚██████╔╝██║     ██║███████╗███████╗██████╔╝██║██║     ██║
 ╚═════╝ ╚═╝     ╚═╝╚══════╝╚══════╝╚═════╝ ╚═╝╚═╝     ╚═╝
`

const APPNAME = "OFileDiff"
const SUITE = "Owl Core Suite"
const TAGLINE = "Text · Binary · Directory Diff & Unified Patch"
const SUBLINE = "Myers/Patience · Moved · Semantic · 3-Way Merge · Patch · Snapshot · HTML"
const VERSION = "1.1"
const AUTHOR = "khaled.s.haddad"
const WEBSITE = "owlsec.org"

// banner prints the cyan ANSI-Shadow wordmark and the suite identity block.
func banner() {
	fmt.Println(col(BANNER, CY))
	fmt.Println("  " + col(SUITE+"  ", B+MG) + col("v"+VERSION, YLW))
	fmt.Println("  " + col(SUBLINE, W))
	fmt.Println()
	fmt.Println("  " + col("Developer : ", DIM) + col(AUTHOR, CY) +
		col("   |   Website : ", DIM) + col(WEBSITE, CY))
	rule()
	fmt.Println()
}

var stdin = bufio.NewReader(os.Stdin)

func prompt(label string) string {
	fmt.Print("  " + col("[»] ", YLW) + col(label+": ", W))
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}
func pause() {
	fmt.Print(col("\n  [↩] Press Enter to continue...", YLW))
	stdin.ReadString('\n')
}

// sectionHeader prints a ⬡ heading between two cyan rules.
func sectionHeader(s string) {
	rule()
	fmt.Println("  " + col("⬡  "+s, B+CY))
	rule()
	fmt.Println()
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if r == '\033' {
			esc = true
			continue
		}
		if esc {
			if r == 'm' {
				esc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func vlen(s string) int { return utf8.RuneCountInString(stripANSI(s)) }
func pad(s string, n int) string {
	if l := vlen(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// table prints aligned columns with a cyan frame.
func table(headers []string, rows [][]string) {
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = vlen(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(w) && vlen(c) > w[i] {
				w[i] = vlen(c)
			}
		}
	}
	var hb strings.Builder
	for i, h := range headers {
		hb.WriteString(col(pad(h, w[i]), CY+B))
		if i < len(headers)-1 {
			hb.WriteString(col(" │ ", CY))
		}
	}
	fmt.Println("  " + hb.String())
	seg := make([]string, len(w))
	for i := range w {
		seg[i] = strings.Repeat("─", w[i])
	}
	fmt.Println("  " + col(strings.Join(seg, "─┼─"), CY))
	for _, r := range rows {
		var rb strings.Builder
		for i := range headers {
			v := ""
			if i < len(r) {
				v = r[i]
			}
			rb.WriteString(pad(col(v, W), w[i]))
			if i < len(headers)-1 {
				rb.WriteString(col(" │ ", CY))
			}
		}
		fmt.Println("  " + rb.String())
	}
}

// ====================================================================
//  OPTIONS
// ====================================================================

type options struct {
	ctx         int
	ignoreAllWS bool // -w
	ignoreWSAmt bool // -b
	ignoreCase  bool // -i
	ignoreBlank bool // -B
	stripCR     bool
	patience    bool // anchor on unique lines (better code diffs)
	colorMoved  bool // highlight blocks that moved
}

var opt = options{ctx: 3, colorMoved: true}

// normalize maps a line to the key used for comparison under the options.
func (o options) normalize(s string) string {
	if o.stripCR {
		s = strings.TrimSuffix(s, "\r")
	}
	switch {
	case o.ignoreAllWS:
		s = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	case o.ignoreWSAmt:
		s = strings.Join(strings.Fields(s), " ")
	}
	if o.ignoreCase {
		s = strings.ToLower(s)
	}
	return s
}

func (o options) String() string {
	var p []string
	p = append(p, fmt.Sprintf("context %d", o.ctx))
	for _, x := range []struct {
		on bool
		n  string
	}{{o.ignoreAllWS, "ignore all space"}, {o.ignoreWSAmt, "ignore space change"}, {o.ignoreCase, "ignore case"},
		{o.ignoreBlank, "ignore blank lines"}, {o.stripCR, "strip CR"}, {o.patience, "patience"}} {
		if x.on {
			p = append(p, x.n)
		}
	}
	return strings.Join(p, " · ")
}

// ====================================================================
//  INPUT
// ====================================================================

type textFile struct {
	name   string
	data   []byte
	lines  []string
	noEOL  bool // last line has no trailing newline
	binary bool
	mtime  time.Time
}

// isBinary follows git: a NUL in the first 8000 bytes.
func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

func splitLines(data []byte) ([]string, bool) {
	if len(data) == 0 {
		return []string{}, false
	}
	s := string(data)
	noEOL := !strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n"), noEOL
}

func newText(name string, data []byte) textFile {
	t := textFile{name: name, data: data, binary: isBinary(data)}
	if !t.binary {
		t.lines, t.noEOL = splitLines(data)
	}
	return t
}

func loadFile(path string) (textFile, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return textFile{}, err
	}
	t := newText(path, data)
	if fi, err := os.Stat(path); err == nil {
		t.mtime = fi.ModTime()
	}
	return t, nil
}

// ====================================================================
//  DIFF ENGINE — linear-space Myers (middle snake)
// ====================================================================

type opKind byte

const (
	opEq  opKind = ' '
	opDel opKind = '-'
	opIns opKind = '+'
)

// edit: one line of the script. ai / bi are the indices consumed so far, so
// an insertion's ai is the position in A where it lands.
type edit struct {
	Kind   opKind
	ai, bi int
}

type differ struct {
	a, b   []int
	ra, rb []bool // changed marks
	vf, vb []int
}

// diffInts returns the change marks of a minimal edit script.
func diffInts(a, b []int) ([]bool, []bool) {
	d := &differ{a: a, b: b, ra: make([]bool, len(a)), rb: make([]bool, len(b))}
	n := len(a) + len(b) + 3
	d.vf, d.vb = make([]int, 2*n+2), make([]int, 2*n+2)
	d.compare(0, len(a), 0, len(b))
	return d.ra, d.rb
}

func (d *differ) compare(aLo, aHi, bLo, bHi int) {
	for aLo < aHi && bLo < bHi && d.a[aLo] == d.b[bLo] {
		aLo++
		bLo++
	}
	for aHi > aLo && bHi > bLo && d.a[aHi-1] == d.b[bHi-1] {
		aHi--
		bHi--
	}
	switch {
	case aLo == aHi:
		for i := bLo; i < bHi; i++ {
			d.rb[i] = true
		}
		return
	case bLo == bHi:
		for i := aLo; i < aHi; i++ {
			d.ra[i] = true
		}
		return
	}
	x, y := d.midSnake(aLo, aHi, bLo, bHi)
	if x == aLo && y == bLo || x == aHi && y == bHi {
		// cannot split further (never expected): mark the block changed
		for i := aLo; i < aHi; i++ {
			d.ra[i] = true
		}
		for i := bLo; i < bHi; i++ {
			d.rb[i] = true
		}
		return
	}
	d.compare(aLo, x, bLo, y)
	d.compare(x, aHi, y, bHi)
}

// midSnake runs the forward and reverse searches until they overlap and
// returns a point on an optimal path. Space is O(N+M).
func (d *differ) midSnake(aLo, aHi, bLo, bHi int) (int, int) {
	N, M := aHi-aLo, bHi-bLo
	delta := N - M
	odd := delta&1 != 0
	max := (N + M + 1) / 2
	off := max + 1
	vf, vb := d.vf[:2*max+3], d.vb[:2*max+3]
	vf[off+1], vb[off+1] = 0, 0
	for D := 0; D <= max; D++ {
		for k := -D; k <= D; k += 2 {
			var x int
			if k == -D || k != D && vf[off+k-1] < vf[off+k+1] {
				x = vf[off+k+1]
			} else {
				x = vf[off+k-1] + 1
			}
			y := x - k
			for x < N && y < M && d.a[aLo+x] == d.b[bLo+y] {
				x++
				y++
			}
			vf[off+k] = x
			if odd {
				if kb := delta - k; kb >= -(D-1) && kb <= D-1 && x+vb[off+kb] >= N {
					return aLo + x, bLo + y
				}
			}
		}
		for k := -D; k <= D; k += 2 {
			var x int
			if k == -D || k != D && vb[off+k-1] < vb[off+k+1] {
				x = vb[off+k+1]
			} else {
				x = vb[off+k-1] + 1
			}
			y := x - k
			for x < N && y < M && d.a[aHi-1-x] == d.b[bHi-1-y] {
				x++
				y++
			}
			vb[off+k] = x
			if !odd {
				if kf := delta - k; kf >= -D && kf <= D && x+vf[off+kf] >= N {
					return aHi - x, bHi - y
				}
			}
		}
	}
	return aLo, bLo
}

// diffLines interns normalized lines and returns the edit script. The last
// line of a file without a trailing newline never equals one with it.
func diffLines(a, b textFile) []edit {
	ids := map[string]int{}
	conv := func(t textFile) []int {
		out := make([]int, len(t.lines))
		for i, l := range t.lines {
			k := opt.normalize(l)
			if t.noEOL && i == len(t.lines)-1 {
				k += "\x00no-eol"
			}
			id, ok := ids[k]
			if !ok {
				id = len(ids)
				ids[k] = id
			}
			out[i] = id
		}
		return out
	}
	xa, xb := conv(a), conv(b)
	if opt.patience {
		return script(diffPatience(xa, xb))
	}
	return script(diffInts(xa, xb))
}

// script turns change marks into an ordered edit list (deletions first in
// each change block, like diff).
func script(ra, rb []bool) []edit {
	var out []edit
	i, j := 0, 0
	for i < len(ra) || j < len(rb) {
		switch {
		case i < len(ra) && ra[i]:
			out = append(out, edit{opDel, i, j})
			i++
		case j < len(rb) && rb[j]:
			out = append(out, edit{opIns, i, j})
			j++
		default:
			out = append(out, edit{opEq, i, j})
			i++
			j++
		}
	}
	return out
}

// ====================================================================
//  HUNKS + UNIFIED OUTPUT
// ====================================================================

type hline struct {
	Op    opKind `json:"op"`
	Text  string `json:"text"`
	NoEOL bool   `json:"no_eol,omitempty"`
	ANo   int    `json:"a_line,omitempty"`
	BNo   int    `json:"b_line,omitempty"`
}

type hunk struct {
	AStart int     `json:"a_start"`
	ALen   int     `json:"a_len"`
	BStart int     `json:"b_start"`
	BLen   int     `json:"b_len"`
	Lines  []hline `json:"lines"`
}

func (h hunk) header() string {
	return fmt.Sprintf("@@ -%s +%s @@", rangeStr(h.AStart, h.ALen), rangeStr(h.BStart, h.BLen))
}

func rangeStr(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// buildHunks groups changes with ctx lines of context. With -B, change
// blocks made only of blank lines do not open a hunk of their own.
func buildHunks(a, b textFile, ed []edit, ctx int) []hunk {
	type block struct{ s, e int }
	var blocks []block
	for i := 0; i < len(ed); {
		if ed[i].Kind == opEq {
			i++
			continue
		}
		s := i
		for i < len(ed) && ed[i].Kind != opEq {
			i++
		}
		if opt.ignoreBlank {
			blank := true
			for _, e := range ed[s:i] {
				t := ""
				if e.Kind == opDel {
					t = a.lines[e.ai]
				} else {
					t = b.lines[e.bi]
				}
				if strings.TrimSpace(t) != "" {
					blank = false
					break
				}
			}
			if blank {
				continue
			}
		}
		blocks = append(blocks, block{s, i})
	}
	var hs []hunk
	for bi := 0; bi < len(blocks); {
		s := max(0, blocks[bi].s-ctx)
		e := blocks[bi].e
		for bi+1 < len(blocks) && blocks[bi+1].s-e <= 2*ctx {
			bi++
			e = blocks[bi].e
		}
		e = min(len(ed), e+ctx)
		bi++
		h := hunk{AStart: ed[s].ai, BStart: ed[s].bi}
		for _, x := range ed[s:e] {
			l := hline{Op: x.Kind}
			switch x.Kind {
			case opEq:
				l.Text, l.ANo, l.BNo = a.lines[x.ai], x.ai+1, x.bi+1
				l.NoEOL = a.noEOL && x.ai == len(a.lines)-1
				h.ALen++
				h.BLen++
			case opDel:
				l.Text, l.ANo = a.lines[x.ai], x.ai+1
				l.NoEOL = a.noEOL && x.ai == len(a.lines)-1
				h.ALen++
			case opIns:
				l.Text, l.BNo = b.lines[x.bi], x.bi+1
				l.NoEOL = b.noEOL && x.bi == len(b.lines)-1
				h.BLen++
			}
			h.Lines = append(h.Lines, l)
		}
		// GNU convention: an empty range names the line before it
		if h.ALen > 0 {
			h.AStart++
		}
		if h.BLen > 0 {
			h.BStart++
		}
		hs = append(hs, h)
	}
	return hs
}

func fileHeader(prefix string, t textFile, label string) string {
	if label == "" {
		label = t.name
	}
	if t.mtime.IsZero() {
		return prefix + " " + label
	}
	return prefix + " " + label + "\t" + t.mtime.Format("2006-01-02 15:04:05.000000000 -0700")
}

// unifiedText renders hunks as a patch body (no colour).
func unifiedText(a, b textFile, la, lb string, hs []hunk) string {
	if len(hs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fileHeader("---", a, la) + "\n" + fileHeader("+++", b, lb) + "\n")
	for _, h := range hs {
		sb.WriteString(h.header() + "\n")
		for _, l := range h.Lines {
			sb.WriteByte(byte(l.Op))
			sb.WriteString(l.Text + "\n")
			if l.NoEOL {
				sb.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return sb.String()
}

// ====================================================================
//  WORD-LEVEL HIGHLIGHT
// ====================================================================

// tokens splits a line into words, whitespace runs and single symbols.
func tokens(s string) []string {
	var out []string
	rs := []rune(s)
	for i := 0; i < len(rs); {
		j := i + 1
		cls := func(r rune) int {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
				return 1
			case unicode.IsSpace(r):
				return 2
			}
			return 3
		}
		c := cls(rs[i])
		if c != 3 {
			for j < len(rs) && cls(rs[j]) == c {
				j++
			}
		}
		out = append(out, string(rs[i:j]))
		i = j
	}
	return out
}

type seg struct {
	text    string
	changed bool
}

// wordDiff marks the tokens that differ between a removed and an added line.
func wordDiff(x, y string) ([]seg, []seg) {
	tx, ty := tokens(x), tokens(y)
	ids := map[string]int{}
	conv := func(ts []string) []int {
		out := make([]int, len(ts))
		for i, t := range ts {
			id, ok := ids[t]
			if !ok {
				id = len(ids)
				ids[t] = id
			}
			out[i] = id
		}
		return out
	}
	ra, rb := diffInts(conv(tx), conv(ty))
	mk := func(ts []string, ch []bool) []seg {
		var out []seg
		for i, t := range ts {
			if n := len(out); n > 0 && out[n-1].changed == ch[i] {
				out[n-1].text += t
			} else {
				out = append(out, seg{t, ch[i]})
			}
		}
		return out
	}
	return mk(tx, ra), mk(ty, rb)
}

// pairs walks a hunk and pairs each removed line with the added line at the
// same offset of the following + block (for word diff and side-by-side).
type sbsRow struct {
	left, right  *hline
	lsegs, rsegs []seg
}

func sideRows(h hunk) []sbsRow {
	var rows []sbsRow
	ls := h.Lines
	for i := 0; i < len(ls); {
		if ls[i].Op == opEq {
			l := ls[i]
			rows = append(rows, sbsRow{left: &l, right: &l})
			i++
			continue
		}
		var dels, ins []hline
		for i < len(ls) && ls[i].Op == opDel {
			dels = append(dels, ls[i])
			i++
		}
		for i < len(ls) && ls[i].Op == opIns {
			ins = append(ins, ls[i])
			i++
		}
		for _, p := range alignBlock(dels, ins) {
			var r sbsRow
			if p[0] >= 0 {
				d := dels[p[0]]
				r.left = &d
			}
			if p[1] >= 0 {
				n := ins[p[1]]
				r.right = &n
			}
			if r.left != nil && r.right != nil {
				r.lsegs, r.rsegs = wordDiff(r.left.Text, r.right.Text)
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// lineSim: share of tokens two lines have in common (0..1).
func lineSim(x, y string) float64 {
	ls, rs := wordDiff(x, y)
	same, total := 0, 0
	for _, s := range ls {
		n := utf8.RuneCountInString(s.text)
		total += n
		if !s.changed {
			same += n
		}
	}
	for _, s := range rs {
		n := utf8.RuneCountInString(s.text)
		total += n
		if !s.changed {
			same += n
		}
	}
	if total == 0 {
		return 1
	}
	return float64(same) / float64(total)
}

// alignBlock pairs the removed and added lines of one change block by
// similarity (order-preserving DP), so an inserted line does not shift
// every pairing after it. Unpaired lines get -1 on the other side. Large
// blocks fall back to positional pairing.
func alignBlock(dels, ins []hline) [][2]int {
	n, m := len(dels), len(ins)
	var out [][2]int
	if n == 0 || m == 0 || n*m > 4000 {
		for k := 0; k < max(n, m); k++ {
			p := [2]int{-1, -1}
			if k < n {
				p[0] = k
			}
			if k < m {
				p[1] = k
			}
			out = append(out, p)
		}
		return out
	}
	const minSim = 0.4
	sim := make([][]float64, n)
	for i := range sim {
		sim[i] = make([]float64, m)
		for j := range sim[i] {
			if v := lineSim(dels[i].Text, ins[j].Text); v >= minSim {
				sim[i][j] = v
			}
		}
	}
	best := make([][]float64, n+1)
	for i := range best {
		best[i] = make([]float64, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			v := math.Max(best[i+1][j], best[i][j+1])
			if sim[i][j] > 0 {
				v = math.Max(v, sim[i][j]+best[i+1][j+1])
			}
			best[i][j] = v
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && sim[i][j] > 0 && best[i][j] == sim[i][j]+best[i+1][j+1]:
			out = append(out, [2]int{i, j})
			i++
			j++
		case j >= m || i < n && best[i][j] == best[i+1][j]:
			out = append(out, [2]int{i, -1})
			i++
		default:
			out = append(out, [2]int{-1, j})
			j++
		}
	}
	return out
}

func renderSegs(ss []seg, base, hl string) string {
	if len(ss) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range ss {
		if s.changed {
			b.WriteString(col(s.text, hl))
		} else {
			b.WriteString(col(s.text, base))
		}
	}
	return b.String()
}

// printUnifiedOrdered prints hunks in true patch order (all - then all + of
// each block) with word highlights from the pairing.
func printUnifiedOrdered(a, b textFile, hs []hunk, limit int) {
	detectMoves(hs)
	fmt.Println("  " + col(fileHeader("---", a, ""), W+B))
	fmt.Println("  " + col(fileHeader("+++", b, ""), W+B))
	if movedBlocks > 0 {
		fmt.Println("  " + col(fmt.Sprintf("%d line(s) moved in %d block(s): ", movedLines, movedBlocks), DIM) +
			col("-moved from", MG) + col(" / ", DIM) + col("+moved to", CY))
	}
	shown := 0
	for _, h := range hs {
		fmt.Println("  " + col(h.header(), CY))
		rows := sideRows(h)
		emit := func(s string, noEOL bool) bool {
			if limit > 0 && shown >= limit {
				return false
			}
			fmt.Println("  " + s)
			if noEOL {
				fmt.Println("  " + col("\\ No newline at end of file", DIM))
			}
			shown++
			return true
		}
		for i := 0; i < len(rows); {
			r := rows[i]
			if r.left != nil && r.left.Op == opEq {
				if !emit(col(" "+r.left.Text, GY), r.left.NoEOL) {
					goto cut
				}
				i++
				continue
			}
			j := i
			for j < len(rows) && !(rows[j].left != nil && rows[j].left.Op == opEq) {
				j++
			}
			for _, x := range rows[i:j] {
				if x.left == nil {
					continue
				}
				s := col("-"+x.left.Text, R)
				switch {
				case isMoved(x.left):
					s = col("-"+x.left.Text, MG)
				case x.lsegs != nil:
					s = col("-", R) + renderSegs(x.lsegs, R, DELHL)
				}
				if !emit(s, x.left.NoEOL) {
					goto cut
				}
			}
			for _, x := range rows[i:j] {
				if x.right == nil {
					continue
				}
				s := col("+"+x.right.Text, GRN)
				switch {
				case isMoved(x.right):
					s = col("+"+x.right.Text, CY)
				case x.rsegs != nil:
					s = col("+", GRN) + renderSegs(x.rsegs, GRN, INSHL)
				}
				if !emit(s, x.right.NoEOL) {
					goto cut
				}
			}
			i = j
		}
	}
	return
cut:
	warnMsg(fmt.Sprintf("output truncated after %d lines — use --output or the patch writer for the rest", limit))
}

func termWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n >= 60 {
		return n
	}
	return 160
}

// printSideBySide shows A and B in two columns with line numbers.
func printSideBySide(a, b textFile, hs []hunk, width int) {
	half := (width - 17) / 2
	if half < 20 {
		half = 20
	}
	cell := func(l *hline, segs []seg, base, hl string, no int) string {
		if l == nil {
			return strings.Repeat(" ", 6+half)
		}
		txt := strings.ReplaceAll(l.Text, "\t", "    ")
		body := col(trunc(txt, half), base)
		if segs != nil && vlen(txt) <= half {
			body = renderSegs(segs, base, hl)
		}
		return col(fmt.Sprintf("%5d ", no), DIM) + pad(body, half)
	}
	detectMoves(hs)
	fmt.Println("  " + col(pad(trunc(a.name, half+6), half+6), W+B) + col("   │ ", CY) + col(trunc(b.name, half+6), W+B))
	for _, h := range hs {
		fmt.Println("  " + col(h.header(), CY))
		for _, r := range sideRows(h) {
			mark := col(" │ ", CY)
			var lt, rt string
			switch {
			case r.left != nil && r.left.Op == opEq:
				lt = cell(r.left, nil, GY, "", r.left.ANo)
				rt = cell(r.right, nil, GY, "", r.right.BNo)
			default:
				ln, rn := 0, 0
				if r.left != nil {
					ln = r.left.ANo
				}
				if r.right != nil {
					rn = r.right.BNo
				}
				lc, rcl := R, GRN
				if isMoved(r.left) {
					lc = MG
				}
				if isMoved(r.right) {
					rcl = CY
				}
				lt = cell(r.left, r.lsegs, lc, DELHL, ln)
				rt = cell(r.right, r.rsegs, rcl, INSHL, rn)
				switch {
				case r.left != nil && r.right != nil:
					mark = col(" ~ ", YLW+B)
				case r.left != nil:
					mark = col(" < ", R+B)
				default:
					mark = col(" > ", GRN+B)
				}
			}
			fmt.Println("  " + lt + mark + rt)
		}
	}
}

// ====================================================================
//  STATS
// ====================================================================

type diffStats struct {
	ALines     int     `json:"a_lines"`
	BLines     int     `json:"b_lines"`
	Unchanged  int     `json:"unchanged"`
	Removed    int     `json:"removed"`
	Added      int     `json:"added"`
	Hunks      int     `json:"hunks"`
	Similarity float64 `json:"similarity"`
}

func statsOf(a, b textFile, ed []edit, hs []hunk) diffStats {
	st := diffStats{ALines: len(a.lines), BLines: len(b.lines), Hunks: len(hs)}
	for _, h := range hs {
		for _, l := range h.Lines {
			switch l.Op {
			case opDel:
				st.Removed++
			case opIns:
				st.Added++
			}
		}
	}
	for _, e := range ed {
		if e.Kind == opEq {
			st.Unchanged++
		}
	}
	st.Similarity = 100
	if n := len(a.lines) + len(b.lines); n > 0 {
		st.Similarity = float64(2*st.Unchanged) / float64(n) * 100
	}
	return st
}

// statLine renders one diffstat row: "name | 12 +++++-----".
func statLine(name string, add, del, width int, colour bool) string {
	n := add + del
	bar := n
	if n > width {
		bar = width
	}
	pa := 0
	if n > 0 {
		pa = (add*bar + n/2) / n
	}
	if add > 0 && pa == 0 {
		pa = 1
	}
	if pa > bar {
		pa = bar
	}
	plus, minus := strings.Repeat("+", pa), strings.Repeat("-", bar-pa)
	if colour {
		plus, minus = col(plus, GRN), col(minus, R)
	}
	return fmt.Sprintf(" %s | %5d %s%s", name, n, plus, minus)
}

// ====================================================================
//  BINARY DIFF
// ====================================================================

type byteRange struct{ S, E int }

type binDiff struct {
	SizeA, SizeB   int
	ShaA, ShaB     string
	Prefix, Suffix int         // equal leading / trailing bytes
	Ranges         []byteRange // differing ranges at the same offset (overlap only)
	DiffBytes      int
}

func diffBinary(a, b []byte) binDiff {
	ha, hb := sha256.Sum256(a), sha256.Sum256(b)
	d := binDiff{SizeA: len(a), SizeB: len(b), ShaA: hex.EncodeToString(ha[:]), ShaB: hex.EncodeToString(hb[:])}
	n := min(len(a), len(b))
	for d.Prefix < n && a[d.Prefix] == b[d.Prefix] {
		d.Prefix++
	}
	for d.Suffix < n-d.Prefix && a[len(a)-1-d.Suffix] == b[len(b)-1-d.Suffix] {
		d.Suffix++
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			d.DiffBytes++
			if k := len(d.Ranges); k > 0 && d.Ranges[k-1].E == i {
				d.Ranges[k-1].E = i + 1
			} else {
				d.Ranges = append(d.Ranges, byteRange{i, i + 1})
			}
		}
	}
	return d
}

// describe explains the change in one sentence (patched in place, insert,
// delete, replace) using the prefix / suffix alignment.
func (d binDiff) describe() string {
	switch {
	case d.ShaA == d.ShaB:
		return "byte-identical"
	case d.SizeA == d.SizeB:
		return fmt.Sprintf("same size; %d byte(s) patched in place across %d range(s)", d.DiffBytes, len(d.Ranges))
	}
	ca, cb := d.SizeA-d.Prefix-d.Suffix, d.SizeB-d.Prefix-d.Suffix
	switch {
	case ca == 0:
		return fmt.Sprintf("%d byte(s) inserted at 0x%X", cb, d.Prefix)
	case cb == 0:
		return fmt.Sprintf("%d byte(s) deleted at 0x%X", ca, d.Prefix)
	}
	return fmt.Sprintf("%d byte(s) at 0x%X replaced by %d byte(s)", ca, d.Prefix, cb)
}

func hexRow(data []byte, off int, other []byte, base, hl string) string {
	var hx, as strings.Builder
	for i := 0; i < 16; i++ {
		p := off + i
		if p >= len(data) {
			hx.WriteString("   ")
			continue
		}
		c := base
		if p >= len(other) || other[p] != data[p] {
			c = hl
		}
		hx.WriteString(col(fmt.Sprintf("%02x", data[p]), c) + " ")
		ch := data[p]
		if ch < 32 || ch > 126 {
			ch = '.'
		}
		as.WriteString(col(string(ch), c))
	}
	return hx.String() + " " + as.String()
}

func printBinary(a, b textFile, rows int) binDiff {
	d := diffBinary(a.data, b.data)
	kv("A", fmt.Sprintf("%s  (%d bytes)", a.name, d.SizeA))
	kv("B", fmt.Sprintf("%s  (%d bytes)", b.name, d.SizeB))
	kv("SHA-256 A", d.ShaA)
	kv("SHA-256 B", d.ShaB)
	kv("Verdict", d.describe())
	if d.ShaA == d.ShaB {
		return d
	}
	kv("Alignment", fmt.Sprintf("equal prefix %d B · equal suffix %d B", d.Prefix, d.Suffix))
	fmt.Println()
	var tr [][]string
	for i, r := range d.Ranges {
		if i >= 20 {
			break
		}
		show := min(8, r.E-r.S)
		tr = append(tr, []string{fmt.Sprintf("0x%08X", r.S), strconv.Itoa(r.E - r.S),
			col(hex.EncodeToString(a.data[r.S:r.S+show]), R), col(hex.EncodeToString(b.data[r.S:r.S+show]), GRN)})
	}
	if len(tr) > 0 {
		table([]string{"OFFSET", "LEN", "A BYTES", "B BYTES"}, tr)
		if len(d.Ranges) > 20 {
			info(fmt.Sprintf("… %d more ranges", len(d.Ranges)-20))
		}
	}
	// hexdump around the first difference
	start := d.Prefix &^ 15
	fmt.Println()
	fmt.Println("  " + col("Hex view from the first difference (A above B)", CY+B))
	for i := 0; i < rows; i++ {
		off := start + i*16
		if off >= len(a.data) && off >= len(b.data) {
			break
		}
		fmt.Println("  " + col(fmt.Sprintf("%08x  A ", off), DIM) + hexRow(a.data, off, b.data, GY, R+B))
		fmt.Println("  " + col(fmt.Sprintf("%08x  B ", off), DIM) + hexRow(b.data, off, a.data, GY, GRN+B))
	}
	return d
}

// ====================================================================
//  DIRECTORY COMPARE
// ====================================================================

type dirEntry struct {
	kind string // file | dir | link | other
	size int64
	path string
	link string
}

type dirResult struct {
	Path   string `json:"path"`
	Status string `json:"status"` // same | changed | only-a | only-b | type | error
	SizeA  int64  `json:"size_a,omitempty"`
	SizeB  int64  `json:"size_b,omitempty"`
	Note   string `json:"note,omitempty"`
}

var excludes []string

func excluded(rel string) bool {
	base := filepath.Base(rel)
	for _, g := range excludes {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
		if ok, _ := filepath.Match(g, filepath.ToSlash(rel)); ok {
			return true
		}
	}
	return false
}

func listTree(root string) (map[string]dirEntry, []string) {
	m := map[string]dirEntry{}
	var warns []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			warns = append(warns, err.Error())
			return nil
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if excluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		e := dirEntry{path: p}
		switch t := d.Type(); {
		case d.IsDir():
			e.kind = "dir"
		case t&fs.ModeSymlink != 0:
			e.kind = "link"
			e.link, _ = os.Readlink(p)
		case t.IsRegular():
			e.kind = "file"
			if fi, err := d.Info(); err == nil {
				e.size = fi.Size()
			}
		default:
			e.kind = "other"
		}
		m[filepath.ToSlash(rel)] = e
		return nil
	})
	return m, warns
}

// sameContent streams both files (sizes already equal) block by block.
func sameContent(pa, pb string) (bool, error) {
	fa, err := os.Open(pa)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(pb)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		if ea == io.EOF || ea == io.ErrUnexpectedEOF {
			return eb == io.EOF || eb == io.ErrUnexpectedEOF, nil
		}
		if ea != nil {
			return false, ea
		}
		if eb != nil {
			return false, eb
		}
	}
}

func compareDirs(ra, rb string) ([]dirResult, []string) {
	ma, wa := listTree(ra)
	mb, wb := listTree(rb)
	keys := map[string]bool{}
	for k := range ma {
		keys[k] = true
	}
	for k := range mb {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var out []dirResult
	for _, k := range ks {
		ea, inA := ma[k]
		eb, inB := mb[k]
		r := dirResult{Path: k, SizeA: ea.size, SizeB: eb.size}
		switch {
		case !inB:
			r.Status = "only-a"
		case !inA:
			r.Status = "only-b"
		case ea.kind != eb.kind:
			r.Status, r.Note = "type", ea.kind+" → "+eb.kind
		case ea.kind == "dir":
			continue
		case ea.kind == "link":
			r.Status = "same"
			if ea.link != eb.link {
				r.Status, r.Note = "changed", ea.link+" → "+eb.link
			}
		case ea.kind == "file":
			if ea.size != eb.size {
				r.Status = "changed"
				break
			}
			same, err := sameContent(ea.path, eb.path)
			switch {
			case err != nil:
				r.Status, r.Note = "error", err.Error()
			case same:
				r.Status = "same"
			default:
				r.Status = "changed"
			}
		default:
			r.Status = "same"
		}
		if (r.Status == "only-a" && ea.kind == "dir") || (r.Status == "only-b" && eb.kind == "dir") {
			r.Note = "dir"
		}
		out = append(out, r)
	}
	return out, append(wa, wb...)
}

// visibleDiffs drops "same" rows and the children of a directory that
// exists on one side only (it is listed once, like diff -rq).
func visibleDiffs(rs []dirResult) []dirResult {
	var out []dirResult
	collapsed := ""
	for _, r := range rs {
		if r.Status == "same" || collapsed != "" && strings.HasPrefix(r.Path, collapsed) {
			continue
		}
		collapsed = ""
		if r.Note == "dir" {
			collapsed = r.Path + "/"
		}
		out = append(out, r)
	}
	return out
}

var statusColor = map[string]string{"same": GY, "changed": YLW, "only-a": R, "only-b": GRN, "type": OR, "error": R + B}

// dirCounts counts files (directories present on one side only are
// represented by the files inside them).
func dirCounts(rs []dirResult) map[string]int {
	c := map[string]int{}
	for _, r := range rs {
		if r.Note != "dir" {
			c[r.Status]++
		}
	}
	return c
}

// dirPatch builds a `diff -ruN` style patch (a/ b/ prefixes, -p1).
func dirPatch(ra, rb string, rs []dirResult) string {
	var sb strings.Builder
	empty := textFile{name: "/dev/null"}
	for _, r := range rs {
		var a, b textFile
		la, lb := "a/"+r.Path, "b/"+r.Path
		switch {
		case r.Status == "changed" && r.Note == "":
			a, _ = loadFile(filepath.Join(ra, r.Path))
			b, _ = loadFile(filepath.Join(rb, r.Path))
		case r.Status == "only-a" && r.Note != "dir":
			a, _ = loadFile(filepath.Join(ra, r.Path))
			b, lb = empty, "/dev/null"
		case r.Status == "only-b" && r.Note != "dir":
			a, la = empty, "/dev/null"
			b, _ = loadFile(filepath.Join(rb, r.Path))
		default:
			continue
		}
		if a.binary || b.binary {
			fmt.Fprintf(&sb, "Binary files %s and %s differ\n", la, lb)
			continue
		}
		hs := buildHunks(a, b, diffLines(a, b), opt.ctx)
		if len(hs) > 0 {
			fmt.Fprintf(&sb, "diff -ruN %s %s\n", la, lb)
			sb.WriteString(unifiedText(a, b, la, lb, hs))
		}
	}
	return sb.String()
}

// ====================================================================
//  PATCH APPLIER
// ====================================================================

type pline struct {
	op    opKind
	text  string
	noEOL bool
}

type phunk struct {
	aStart, aLen, bStart, bLen int
	lines                      []pline
}

type filePatch struct {
	oldName, newName string
	hunks            []phunk
}

func parseRange(s string) (int, int, error) {
	st, n, ok := strings.Cut(s, ",")
	a, err := strconv.Atoi(st)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return a, 1, nil
	}
	b, err := strconv.Atoi(n)
	return a, b, err
}

func headerName(l string) string {
	n := strings.TrimSpace(l[4:])
	if i := strings.IndexByte(n, '\t'); i >= 0 {
		n = n[:i]
	}
	return n
}

// parsePatch reads one or more unified diffs (git / diff -ruN output).
func parsePatch(text string) ([]filePatch, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []filePatch
	var cur *filePatch
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			out = append(out, filePatch{oldName: headerName(l), newName: headerName(lines[i+1])})
			cur = &out[len(out)-1]
			i++
		case strings.HasPrefix(l, "@@ "):
			if cur == nil {
				return nil, fmt.Errorf("line %d: hunk before any file header", i+1)
			}
			f := strings.Fields(l)
			if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
				return nil, fmt.Errorf("line %d: bad hunk header %q", i+1, l)
			}
			var h phunk
			var e1, e2 error
			h.aStart, h.aLen, e1 = parseRange(f[1][1:])
			h.bStart, h.bLen, e2 = parseRange(f[2][1:])
			if e1 != nil || e2 != nil {
				return nil, fmt.Errorf("line %d: bad hunk header %q", i+1, l)
			}
			na, nb := 0, 0
			for na < h.aLen || nb < h.bLen {
				i++
				if i >= len(lines) {
					return nil, fmt.Errorf("hunk %s truncated", f[1]+" "+f[2])
				}
				hl := lines[i]
				if strings.HasPrefix(hl, "\\") {
					if n := len(h.lines); n > 0 {
						h.lines[n-1].noEOL = true
					}
					continue
				}
				op := opEq
				if hl != "" {
					op = opKind(hl[0])
					hl = hl[1:]
				}
				switch op {
				case opEq:
					na++
					nb++
				case opDel:
					na++
				case opIns:
					nb++
				default:
					return nil, fmt.Errorf("line %d: unexpected %q inside a hunk", i+1, lines[i])
				}
				h.lines = append(h.lines, pline{op, hl, false})
			}
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\\") {
				i++
				h.lines[len(h.lines)-1].noEOL = true
			}
			cur.hunks = append(cur.hunks, h)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no unified diff found")
	}
	return out, nil
}

func (h phunk) reversed() phunk {
	r := phunk{aStart: h.bStart, aLen: h.bLen, bStart: h.aStart, bLen: h.aLen}
	for _, l := range h.lines {
		switch l.op {
		case opDel:
			l.op = opIns
		case opIns:
			l.op = opDel
		}
		r.lines = append(r.lines, l)
	}
	return r
}

type applyResult struct {
	lines   []string
	noEOL   bool
	offsets []int // per hunk: how far it moved from the stated position
}

// applyHunks applies hunks in order. Each hunk must match exactly; if not at
// its stated line it is searched outward (like patch's offset, no fuzz).
func applyHunks(orig textFile, hs []phunk) (applyResult, error) {
	var res applyResult
	cursor, shift := 0, 0
	tailNoEOL := false
	for hi, h := range hs {
		var old, nw []pline
		for _, l := range h.lines {
			if l.op != opIns {
				old = append(old, l)
			}
			if l.op != opDel {
				nw = append(nw, l)
			}
		}
		want := h.aStart - 1
		if h.aLen == 0 {
			want = h.aStart
		}
		want += shift
		matches := func(p int) bool {
			if p < cursor || p+len(old) > len(orig.lines) {
				return false
			}
			for k, l := range old {
				if orig.lines[p+k] != l.text {
					return false
				}
				last := p+k == len(orig.lines)-1
				if l.noEOL != (last && orig.noEOL) {
					return false
				}
			}
			return true
		}
		pos := -1
		for d := 0; d <= len(orig.lines); d++ {
			if matches(want - d) {
				pos = want - d
				break
			}
			if d > 0 && matches(want+d) {
				pos = want + d
				break
			}
		}
		if pos < 0 {
			return res, fmt.Errorf("hunk #%d (%s) does not apply", hi+1, fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.aStart, h.aLen, h.bStart, h.bLen))
		}
		res.offsets = append(res.offsets, pos-want)
		shift += pos - want
		res.lines = append(res.lines, orig.lines[cursor:pos]...)
		for _, l := range nw {
			res.lines = append(res.lines, l.text)
		}
		cursor = pos + len(old)
		tailNoEOL = len(nw) > 0 && nw[len(nw)-1].noEOL
	}
	if cursor < len(orig.lines) {
		res.lines = append(res.lines, orig.lines[cursor:]...)
		res.noEOL = orig.noEOL
	} else if len(hs) > 0 {
		res.noEOL = tailNoEOL
	} else {
		res.noEOL = orig.noEOL
	}
	return res, nil
}

func (r applyResult) bytes() []byte {
	if len(r.lines) == 0 {
		return nil
	}
	s := strings.Join(r.lines, "\n")
	if !r.noEOL {
		s += "\n"
	}
	return []byte(s)
}

func stripPath(p string, n int) string {
	p = filepath.ToSlash(p)
	for i := 0; i < n; i++ {
		j := strings.IndexByte(p, '/')
		if j < 0 {
			break
		}
		p = p[j+1:]
	}
	return p
}

type applyReport struct {
	file, action string
	hunks        int
	offsets      []int
	err          error
}

// applyPatch applies every file of a patch under dir. Paths that escape dir
// (absolute or ..) are refused — a malicious patch cannot write elsewhere.
func applyPatch(text, dir string, strip int, reverse, dry bool) ([]applyReport, error) {
	fps, err := parsePatch(text)
	if err != nil {
		return nil, err
	}
	var out []applyReport
	for _, fp := range fps {
		oldN, newN := fp.oldName, fp.newName
		hs := fp.hunks
		if reverse {
			oldN, newN = newN, oldN
			for i := range hs {
				hs[i] = hs[i].reversed()
			}
		}
		create, del := oldN == "/dev/null", newN == "/dev/null"
		target := newN
		if del {
			target = oldN
		}
		rel := stripPath(target, strip)
		rep := applyReport{file: rel, hunks: len(hs)}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			rep.err = errors.New("refused: path escapes the target directory")
			out = append(out, rep)
			continue
		}
		path := filepath.Join(dir, clean)
		var orig textFile
		if !create {
			orig, err = loadFile(path)
			if err != nil {
				rep.err = err
				out = append(out, rep)
				continue
			}
		} else if _, err := os.Stat(path); err == nil {
			rep.err = errors.New("refused: file to create already exists")
			out = append(out, rep)
			continue
		}
		res, err := applyHunks(orig, hs)
		rep.offsets = res.offsets
		if err != nil {
			rep.err = err
			out = append(out, rep)
			continue
		}
		switch {
		case del:
			rep.action = "delete"
			if len(res.lines) > 0 {
				rep.err = errors.New("deletion patch leaves content behind")
			}
		case create:
			rep.action = "create"
		default:
			rep.action = "patch"
		}
		if rep.err == nil && !dry {
			switch {
			case del:
				rep.err = os.Remove(path)
				// like patch: drop parent directories the deletion left empty
				root, _ := filepath.Abs(dir)
				for d := filepath.Dir(path); rep.err == nil; d = filepath.Dir(d) {
					if ad, _ := filepath.Abs(d); ad == root || len(ad) <= len(root) || os.Remove(d) != nil {
						break
					}
				}
			default:
				mode := os.FileMode(0o644)
				if fi, err := os.Stat(path); err == nil {
					mode = fi.Mode().Perm()
				}
				if create {
					rep.err = os.MkdirAll(filepath.Dir(path), 0o755)
				}
				if rep.err == nil {
					rep.err = os.WriteFile(path, res.bytes(), mode)
				}
			}
		}
		out = append(out, rep)
	}
	return out, nil
}

func printApply(reps []applyReport, dry bool) int {
	failed := 0
	for _, r := range reps {
		switch {
		case r.err != nil:
			failed++
			errMsg(fmt.Sprintf("%s: %v", r.file, r.err))
		default:
			var off []string
			for i, o := range r.offsets {
				if o != 0 {
					off = append(off, fmt.Sprintf("#%d %+d", i+1, o))
				}
			}
			msg := fmt.Sprintf("%s: %s, %d hunk(s)", r.file, r.action, r.hunks)
			if len(off) > 0 {
				msg += " — offsets " + strings.Join(off, ", ")
			}
			if dry {
				msg += " (dry run)"
			}
			okMsg(msg)
		}
	}
	return failed
}

// ====================================================================
//  PATIENCE DIFF (unique-line anchors, Myers between them)
// ====================================================================

// diffPatience anchors on lines that occur exactly once on both sides, keeps
// the longest increasing run of them, and diffs the gaps with Myers. Code
// diffs line up on function signatures instead of on stray "}" lines.
func diffPatience(a, b []int) ([]bool, []bool) {
	ra, rb := make([]bool, len(a)), make([]bool, len(b))
	var rec func(aLo, aHi, bLo, bHi, depth int)
	rec = func(aLo, aHi, bLo, bHi, depth int) {
		for aLo < aHi && bLo < bHi && a[aLo] == b[bLo] {
			aLo++
			bLo++
		}
		for aHi > aLo && bHi > bLo && a[aHi-1] == b[bHi-1] {
			aHi--
			bHi--
		}
		if aLo == aHi || bLo == bHi || depth > 64 {
			myersInto(a, b, ra, rb, aLo, aHi, bLo, bHi)
			return
		}
		ca, cb := map[int]int{}, map[int]int{}
		pa, pb := map[int]int{}, map[int]int{}
		for i := aLo; i < aHi; i++ {
			ca[a[i]]++
			pa[a[i]] = i
		}
		for j := bLo; j < bHi; j++ {
			cb[b[j]]++
			pb[b[j]] = j
		}
		// unique-in-both lines in A order, with their B position
		type pr struct{ i, j int }
		var cand []pr
		for i := aLo; i < aHi; i++ {
			if ca[a[i]] == 1 && cb[a[i]] == 1 {
				cand = append(cand, pr{i, pb[a[i]]})
			}
		}
		if len(cand) == 0 {
			myersInto(a, b, ra, rb, aLo, aHi, bLo, bHi)
			return
		}
		// longest increasing subsequence on j (patience sorting)
		tails, prev := []int{}, make([]int, len(cand))
		for k, c := range cand {
			lo, hi := 0, len(tails)
			for lo < hi {
				m := (lo + hi) / 2
				if cand[tails[m]].j < c.j {
					lo = m + 1
				} else {
					hi = m
				}
			}
			if lo > 0 {
				prev[k] = tails[lo-1]
			} else {
				prev[k] = -1
			}
			if lo == len(tails) {
				tails = append(tails, k)
			} else {
				tails[lo] = k
			}
		}
		var anchors []pr
		for k := tails[len(tails)-1]; k >= 0; k = prev[k] {
			anchors = append([]pr{cand[k]}, anchors...)
		}
		i, j := aLo, bLo
		for _, an := range anchors {
			rec(i, an.i, j, an.j, depth+1)
			i, j = an.i+1, an.j+1
		}
		rec(i, aHi, j, bHi, depth+1)
	}
	rec(0, len(a), 0, len(b), 0)
	return ra, rb
}

// myersInto runs the Myers engine on a sub-range and copies its marks.
func myersInto(a, b []int, ra, rb []bool, aLo, aHi, bLo, bHi int) {
	if aLo == aHi && bLo == bHi {
		return
	}
	xa, xb := diffInts(a[aLo:aHi], b[bLo:bHi])
	copy(ra[aLo:aHi], xa)
	copy(rb[bLo:bHi], xb)
}

// ====================================================================
//  MOVED-LINE DETECTION (like git --color-moved)
// ====================================================================

// moved holds "a:N" / "b:N" line numbers (1-based) that belong to a block
// deleted in one place and inserted unchanged in another.
var moved = map[string]bool{}
var movedBlocks, movedLines int

const movedMinLines, movedMinChars = 3, 20

// detectMoves finds maximal runs of consecutive removed lines whose text
// reappears as consecutive added lines elsewhere. A run counts when it has
// ≥ 3 lines or ≥ 20 alphanumeric characters, so stray "}" lines do not.
func detectMoves(hs []hunk) {
	moved, movedBlocks, movedLines = map[string]bool{}, 0, 0
	if !opt.colorMoved {
		return
	}
	key := func(s string) string { return opt.normalize(strings.TrimSpace(s)) }
	delText, insText := map[int]string{}, map[int]string{}
	var dels []int
	insAt := map[string][]int{}
	for _, h := range hs {
		for _, l := range h.Lines {
			switch l.Op {
			case opDel:
				delText[l.ANo] = key(l.Text)
				dels = append(dels, l.ANo)
			case opIns:
				insText[l.BNo] = key(l.Text)
				if k := key(l.Text); k != "" {
					insAt[k] = append(insAt[k], l.BNo)
				}
			}
		}
	}
	usedB := map[int]bool{}
	for p := 0; p < len(dels); {
		i := dels[p]
		best, bestJ := 0, -1
		for _, j := range insAt[delText[i]] {
			n := 0
			for {
				dt, okD := delText[i+n]
				it, okI := insText[j+n]
				if !okD || !okI || usedB[j+n] || dt != it || dt == "" {
					break
				}
				n++
			}
			if n > best {
				best, bestJ = n, j
			}
		}
		chars := 0
		for x := 0; x < best; x++ {
			for _, r := range delText[i+x] {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					chars++
				}
			}
		}
		if best > 0 && (best >= movedMinLines || chars >= movedMinChars) {
			for x := 0; x < best; x++ {
				moved[fmt.Sprintf("a:%d", i+x)] = true
				moved[fmt.Sprintf("b:%d", bestJ+x)] = true
				usedB[bestJ+x] = true
			}
			movedBlocks++
			movedLines += best
			p += best
			continue
		}
		p++
	}
}

func isMoved(l *hline) bool {
	if l == nil {
		return false
	}
	switch l.Op {
	case opDel:
		return moved[fmt.Sprintf("a:%d", l.ANo)]
	case opIns:
		return moved[fmt.Sprintf("b:%d", l.BNo)]
	}
	return false
}

// ====================================================================
//  3-WAY MERGE (diff3)
// ====================================================================

type mergeResult struct {
	lines     []string
	noEOL     bool
	conflicts int
	clean     int // chunks merged automatically
}

// matchMap returns, for each base line, the index of the equal line in x
// (-1 when the line was changed or deleted).
func matchMap(base, x textFile) []int {
	m := make([]int, len(base.lines))
	for i := range m {
		m[i] = -1
	}
	for _, e := range diffLines(base, x) {
		if e.Kind == opEq {
			m[e.ai] = e.bi
		}
	}
	return m
}

// merge3 walks the base: lines kept by both sides are stable; between them
// a chunk changed on one side takes that side, identical changes merge, and
// different changes become a conflict with diff3-style markers.
func merge3(base, a, b textFile, la, lb string, showBase bool) mergeResult {
	ma, mb := matchMap(base, a), matchMap(base, b)
	var r mergeResult
	i, j, k := 0, 0, 0
	lastFrom := "a"
	eq := func(x, y []string) bool {
		if len(x) != len(y) {
			return false
		}
		for n := range x {
			if x[n] != y[n] {
				return false
			}
		}
		return true
	}
	for {
		// next base line that both sides kept, in order
		x := i
		for x < len(base.lines) && !(ma[x] >= j && mb[x] >= k) {
			x++
		}
		if x == i && x < len(base.lines) && ma[x] == j && mb[x] == k {
			r.lines = append(r.lines, base.lines[x])
			i, j, k = x+1, j+1, k+1
			lastFrom = "base"
			continue
		}
		ae, be := len(a.lines), len(b.lines)
		if x < len(base.lines) {
			ae, be = ma[x], mb[x]
		}
		bs, as, bbs := base.lines[i:x], a.lines[j:ae], b.lines[k:be]
		switch {
		case len(bs) == 0 && len(as) == 0 && len(bbs) == 0:
		case eq(as, bs):
			r.lines = append(r.lines, bbs...)
			r.clean++
			lastFrom = "b"
		case eq(bbs, bs) || eq(as, bbs):
			r.lines = append(r.lines, as...)
			r.clean++
			lastFrom = "a"
		default:
			r.conflicts++
			r.lines = append(r.lines, "<<<<<<< "+la)
			r.lines = append(r.lines, as...)
			if showBase {
				r.lines = append(r.lines, "||||||| base")
				r.lines = append(r.lines, bs...)
			}
			r.lines = append(r.lines, "=======")
			r.lines = append(r.lines, bbs...)
			r.lines = append(r.lines, ">>>>>>> "+lb)
			lastFrom = "conflict"
		}
		if x >= len(base.lines) {
			break
		}
		i, j, k = x, ae, be
	}
	switch lastFrom {
	case "b":
		r.noEOL = b.noEOL
	case "a", "base":
		r.noEOL = a.noEOL && b.noEOL || lastFrom == "a" && a.noEOL
	}
	return r
}

func (r mergeResult) bytes() []byte {
	if len(r.lines) == 0 {
		return nil
	}
	s := strings.Join(r.lines, "\n")
	if !r.noEOL {
		s += "\n"
	}
	return []byte(s)
}

func printMerge(r mergeResult, limit int) {
	n := 0
	for _, l := range r.lines {
		if limit > 0 && n >= limit {
			warnMsg(fmt.Sprintf("… %d more lines (use --output)", len(r.lines)-n))
			break
		}
		n++
		switch {
		case strings.HasPrefix(l, "<<<<<<< ") || strings.HasPrefix(l, ">>>>>>> ") || l == "=======" || strings.HasPrefix(l, "||||||| "):
			fmt.Println("  " + col(l, R+B))
		default:
			fmt.Println("  " + col(l, W))
		}
	}
}

// ====================================================================
//  SNAPSHOT / MANIFEST (integrity baseline)
// ====================================================================

type manifestEntry struct {
	Kind   string `json:"kind"` // file | link | dir
	Size   int64  `json:"size,omitempty"`
	Mode   string `json:"mode"`
	MTime  string `json:"mtime,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
}

type manifest struct {
	Tool    string                   `json:"tool"`
	Version string                   `json:"version"`
	Root    string                   `json:"root"`
	Created string                   `json:"created"`
	Files   map[string]manifestEntry `json:"files"`
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func snapshot(root string) (manifest, []string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return manifest{}, nil, err
	}
	m := manifest{Tool: APPNAME, Version: VERSION, Root: abs, Created: time.Now().UTC().Format(time.RFC3339), Files: map[string]manifestEntry{}}
	tree, warns := listTree(root)
	for rel, e := range tree {
		me := manifestEntry{Kind: e.kind}
		if fi, err := os.Lstat(e.path); err == nil {
			me.Mode = fmt.Sprintf("%04o", fi.Mode().Perm())
			if e.kind == "file" {
				me.MTime = fi.ModTime().UTC().Format(time.RFC3339)
			}
		}
		switch e.kind {
		case "file":
			me.Size = e.size
			h, err := hashFile(e.path)
			if err != nil {
				warns = append(warns, err.Error())
				me.SHA256 = "unreadable"
			} else {
				me.SHA256 = h
			}
		case "link":
			me.Link = e.link
		}
		m.Files[rel] = me
	}
	return m, warns, nil
}

type verifyRow struct {
	Path   string `json:"path"`
	Status string `json:"status"` // modified | added | removed | mode | type | link | unreadable
	Detail string `json:"detail,omitempty"`
}

func loadManifest(p string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(p)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Tool != APPNAME || m.Files == nil {
		return m, fmt.Errorf("%s is not an %s manifest", p, APPNAME)
	}
	return m, nil
}

// verifyManifest compares a directory against a snapshot. Content is judged
// by SHA-256 only; a touched-but-identical file is not a change.
func verifyManifest(m manifest, root string) ([]verifyRow, int, error) {
	cur, _, err := snapshot(root)
	if err != nil {
		return nil, 0, err
	}
	var out []verifyRow
	keys := map[string]bool{}
	for k := range m.Files {
		keys[k] = true
	}
	for k := range cur.Files {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	same := 0
	for _, k := range ks {
		o, inO := m.Files[k]
		n, inN := cur.Files[k]
		switch {
		case !inN:
			out = append(out, verifyRow{k, "removed", o.Kind})
		case !inO:
			out = append(out, verifyRow{k, "added", fmt.Sprintf("%s %d B", n.Kind, n.Size)})
		case o.Kind != n.Kind:
			out = append(out, verifyRow{k, "type", o.Kind + " → " + n.Kind})
		case n.SHA256 == "unreadable":
			out = append(out, verifyRow{k, "unreadable", "cannot hash now"})
		case o.SHA256 != n.SHA256:
			out = append(out, verifyRow{k, "modified", fmt.Sprintf("%d → %d B · %s → %s", o.Size, n.Size, short(o.SHA256), short(n.SHA256))})
		case o.Link != n.Link:
			out = append(out, verifyRow{k, "link", o.Link + " → " + n.Link})
		case o.Mode != n.Mode:
			out = append(out, verifyRow{k, "mode", o.Mode + " → " + n.Mode})
		default:
			if o.Kind != "dir" {
				same++
			}
		}
	}
	return out, same, nil
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

var verifyColor = map[string]string{"modified": YLW, "added": GRN, "removed": R, "mode": OR, "type": OR, "link": OR, "unreadable": R + B}

func printVerify(m manifest, root string, rows []verifyRow, same int) {
	kv("Manifest", fmt.Sprintf("%s · %d entries · created %s", m.Root, len(m.Files), m.Created))
	kv("Checked", root)
	c := map[string]int{}
	for _, r := range rows {
		c[r.Status]++
	}
	var cs []string
	for _, s := range []string{"modified", "added", "removed", "mode", "type", "link", "unreadable"} {
		if c[s] > 0 {
			cs = append(cs, col(fmt.Sprintf("%s %d", s, c[s]), verifyColor[s]))
		}
	}
	cs = append(cs, col(fmt.Sprintf("unchanged %d", same), GY))
	kv("Result", strings.Join(cs, col(" · ", DIM)))
	fmt.Println()
	if len(rows) == 0 {
		okMsg("Integrity verified — everything matches the snapshot.")
		return
	}
	var tr [][]string
	for _, r := range rows {
		tr = append(tr, []string{col(strings.ToUpper(r.Status), verifyColor[r.Status]), trunc(r.Path, 56), trunc(r.Detail, 50)})
	}
	table([]string{"STATUS", "PATH", "DETAIL"}, tr)
}

// ====================================================================
//  SEMANTIC DIFF — JSON / YAML compared as data, not text
// ====================================================================

// parseData reads JSON, or a YAML subset (block mappings / sequences,
// scalars, quotes, comments, flow [..] {..}, | and > blocks, documents
// separated by ---) — enough for config files, manifests and CI files.
func parseData(name string, data []byte) (any, error) {
	t := bytes.TrimSpace(data)
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".json" || len(t) > 0 && (t[0] == '{' || t[0] == '[') && ext != ".yaml" && ext != ".yml" {
		dec := json.NewDecoder(bytes.NewReader(t))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("%s: JSON: %v", name, err)
		}
		return v, nil
	}
	v, err := parseYAML(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: YAML: %v", name, err)
	}
	return v, nil
}

type yline struct {
	indent int
	text   string
	no     int
}

func parseYAML(src string) (any, error) {
	var docs []any
	var cur []yline
	flush := func() error {
		if len(cur) == 0 {
			return nil
		}
		p := &yparser{lines: cur}
		v, err := p.block(cur[0].indent)
		if err != nil {
			return err
		}
		if p.i < len(p.lines) {
			return fmt.Errorf("line %d: unexpected indentation", p.lines[p.i].no)
		}
		docs = append(docs, v)
		cur = nil
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for n := 0; n < len(lines); n++ {
		raw := lines[n]
		if strings.HasPrefix(raw, "---") || strings.HasPrefix(raw, "...") {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]; strings.Contains(lead, "\t") && strings.TrimSpace(raw) != "" {
			return nil, fmt.Errorf("line %d: tabs are not allowed for indentation", n+1)
		}
		s := stripComment(raw)
		if strings.TrimSpace(s) == "" {
			continue
		}
		ind := len(s) - len(strings.TrimLeft(s, " "))
		body := strings.TrimSpace(s)
		// literal / folded block scalars keep their raw lines
		if strings.HasSuffix(body, "|") || strings.HasSuffix(body, "|-") || strings.HasSuffix(body, ">") || strings.HasSuffix(body, ">-") {
			marker := body[strings.LastIndexAny(body, "|>"):]
			var blk []string
			bi := -1
			for n+1 < len(lines) {
				nx := lines[n+1]
				if strings.TrimSpace(nx) == "" {
					blk = append(blk, "")
					n++
					continue
				}
				ni := len(nx) - len(strings.TrimLeft(nx, " "))
				if ni <= ind {
					break
				}
				if bi < 0 {
					bi = ni
				}
				blk = append(blk, nx[min(bi, ni):])
				n++
			}
			for len(blk) > 0 && blk[len(blk)-1] == "" {
				blk = blk[:len(blk)-1]
			}
			val := strings.Join(blk, "\n")
			if marker[0] == '>' { // folded: lines join with spaces, blank lines become newlines
				var fb strings.Builder
				prevText := false
				for _, l := range blk {
					switch {
					case l == "":
						fb.WriteByte('\n')
						prevText = false
					case prevText:
						fb.WriteString(" " + l)
					default:
						fb.WriteString(l)
						prevText = true
					}
				}
				val = fb.String()
			}
			if !strings.HasSuffix(marker, "-") {
				val += "\n"
			}
			body = strings.TrimSpace(body[:len(body)-len(marker)]) + " " + strconv.Quote(val)
		}
		cur = append(cur, yline{ind, body, n + 1})
	}
	if err := flush(); err != nil {
		return nil, err
	}
	switch len(docs) {
	case 0:
		return nil, nil
	case 1:
		return docs[0], nil
	}
	out := make([]any, len(docs))
	copy(out, docs)
	return map[string]any{"(documents)": out}, nil
}

// stripComment drops " # …" outside quotes.
func stripComment(s string) string {
	q := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return s[:i]
		}
	}
	return s
}

type yparser struct {
	lines []yline
	i     int
}

func (p *yparser) block(ind int) (any, error) {
	if p.i >= len(p.lines) {
		return nil, nil
	}
	t := p.lines[p.i].text
	switch {
	case strings.HasPrefix(t, "- ") || t == "-":
		return p.seq(ind)
	case !isMapStart(t):
		// a multi-line plain scalar under "key:" ("host:443" is not a key)
		var parts []string
		for p.i < len(p.lines) && p.lines[p.i].indent >= ind {
			parts = append(parts, p.lines[p.i].text)
			p.i++
		}
		return scalar(strings.Join(parts, " "))
	}
	return p.mapping(ind)
}

// continuation folds lines indented deeper than ind into an inline value
// (plain or quoted scalars may wrap; nothing can nest under them).
func (p *yparser) continuation(v string, ind int) string {
	for p.i < len(p.lines) && p.lines[p.i].indent > ind {
		v += " " + p.lines[p.i].text
		p.i++
	}
	return v
}

func (p *yparser) seq(ind int) (any, error) {
	out := []any{}
	for p.i < len(p.lines) && p.lines[p.i].indent == ind && (strings.HasPrefix(p.lines[p.i].text, "- ") || p.lines[p.i].text == "-") {
		l := p.lines[p.i]
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		p.i++
		switch {
		case rest == "":
			if p.i < len(p.lines) && p.lines[p.i].indent > ind {
				v, err := p.block(p.lines[p.i].indent)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			} else {
				out = append(out, nil)
			}
		case isMapStart(rest):
			// "- key: value" starts a mapping whose keys align after "- "
			inner := ind + len(l.text) - len(rest) // keys align after "- "
			p.i--
			p.lines[p.i] = yline{inner, rest, l.no}
			v, err := p.mapping(inner)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		default:
			v, err := scalar(p.continuation(rest, ind))
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", l.no, err)
			}
			out = append(out, v)
		}
	}
	return out, nil
}

func isMapStart(s string) bool {
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") || strings.HasPrefix(s, `"`) && !strings.Contains(s, `":`) {
		return false
	}
	_, _, ok := splitKey(s)
	return ok
}

// splitKey splits "key: value" (the colon must be followed by space / EOL).
func splitKey(s string) (string, string, bool) {
	q := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			if i == 0 {
				q = c
			}
		case c == ':' && (i+1 == len(s) || s[i+1] == ' '):
			k := strings.TrimSpace(s[:i])
			if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') && k[len(k)-1] == k[0] {
				k = k[1 : len(k)-1]
			}
			return k, strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

func (p *yparser) mapping(ind int) (any, error) {
	out := map[string]any{}
	for p.i < len(p.lines) && p.lines[p.i].indent == ind {
		l := p.lines[p.i]
		if strings.HasPrefix(l.text, "- ") {
			break
		}
		k, v, ok := splitKey(l.text)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", l.no)
		}
		p.i++
		if v == "" {
			if p.i < len(p.lines) && (p.lines[p.i].indent > ind || p.lines[p.i].indent == ind && strings.HasPrefix(p.lines[p.i].text, "- ")) {
				x, err := p.block(p.lines[p.i].indent)
				if err != nil {
					return nil, err
				}
				out[k] = x
			} else {
				out[k] = nil
			}
			continue
		}
		x, err := scalar(p.continuation(v, ind))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", l.no, err)
		}
		out[k] = x
	}
	return out, nil
}

// scalar interprets a YAML scalar or a flow collection.
func scalar(s string) (any, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "~" || s == "null" || s == "Null" || s == "NULL":
		return nil, nil
	case strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*"):
		return s, nil // anchors / aliases are compared as written
	case s[0] == '"':
		v, err := strconv.Unquote(s)
		if err != nil {
			return strings.Trim(s, `"`), nil
		}
		return v, nil
	case s[0] == '\'':
		return strings.ReplaceAll(strings.Trim(s, "'"), "''", "'"), nil
	case s[0] == '[' || s[0] == '{':
		return flow(s)
	}
	// YAML 1.2 core schema: only true / false are booleans ("on", "yes" stay
	// strings — GitHub's `on:` key), 0x / 0o integers are numbers
	switch s {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0o") {
		base, digits := 16, s[2:]
		if s[1] == 'o' {
			base = 8
		}
		if n, err := strconv.ParseUint(digits, base, 64); err == nil {
			return json.Number(strconv.FormatUint(n, 10)), nil
		}
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return json.Number(s), nil
	}
	return s, nil
}

// flow parses [a, b] and {k: v} (nested) by converting to JSON-ish tokens.
func flow(s string) (any, error) {
	var parse func() (any, error)
	i := 0
	skip := func() {
		for i < len(s) && s[i] == ' ' {
			i++
		}
	}
	item := func(stop string) string {
		start := i
		q := byte(0)
		for i < len(s) {
			c := s[i]
			if q != 0 {
				if c == q {
					q = 0
				}
			} else if c == '"' || c == '\'' {
				q = c
			} else if strings.IndexByte(stop, c) >= 0 {
				break
			}
			i++
		}
		return strings.TrimSpace(s[start:i])
	}
	parse = func() (any, error) {
		skip()
		if i >= len(s) {
			return nil, errors.New("unterminated flow collection")
		}
		switch s[i] {
		case '[':
			i++
			out := []any{}
			for {
				skip()
				if i < len(s) && s[i] == ']' {
					i++
					return out, nil
				}
				var v any
				var err error
				if i < len(s) && (s[i] == '[' || s[i] == '{') {
					v, err = parse()
				} else {
					v, err = scalar(item(",]"))
				}
				if err != nil {
					return nil, err
				}
				out = append(out, v)
				skip()
				if i < len(s) && s[i] == ',' {
					i++
				}
			}
		case '{':
			i++
			out := map[string]any{}
			for {
				skip()
				if i < len(s) && s[i] == '}' {
					i++
					return out, nil
				}
				k := strings.Trim(item(":,}"), `"'`)
				if i >= len(s) || s[i] != ':' {
					return nil, errors.New("flow mapping needs key: value")
				}
				i++
				skip()
				var v any
				var err error
				if i < len(s) && (s[i] == '[' || s[i] == '{') {
					v, err = parse()
				} else {
					v, err = scalar(item(",}"))
				}
				if err != nil {
					return nil, err
				}
				out[k] = v
				skip()
				if i < len(s) && s[i] == ',' {
					i++
				}
			}
		}
		return scalar(item(",]}"))
	}
	return parse()
}

type semChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // added | removed | changed | type
	Old  any    `json:"old,omitempty"`
	New  any    `json:"new,omitempty"`
}

// identity keys used to match list items (k8s containers, env vars, ports …)
var idKeys = []string{"name", "id", "key", "uid", "metadata.name"}

func itemID(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	for _, k := range idKeys {
		if k == "metadata.name" {
			if md, ok := m["metadata"].(map[string]any); ok {
				if s, ok := md["name"].(string); ok {
					return s, true
				}
			}
			continue
		}
		if x, ok := m[k]; ok {
			if s := fmt.Sprint(x); s != "" {
				return k + "=" + s, true
			}
		}
	}
	return "", false
}

func joinPath(p, k string) string {
	if p == "" {
		return k
	}
	if strings.HasPrefix(k, "[") {
		return p + k
	}
	return p + "." + k
}

func kindOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// semDiff compares two decoded documents. Object keys are matched by name
// (order is irrelevant); arrays of objects with an identity key (name, id …)
// are matched by that key, other arrays by position.
func semDiff(path string, a, b any, out *[]semChange) {
	if kindOf(a) != kindOf(b) {
		if a == nil {
			*out = append(*out, semChange{path, "added", nil, b})
		} else if b == nil {
			*out = append(*out, semChange{path, "removed", a, nil})
		} else {
			*out = append(*out, semChange{path, "type", a, b})
		}
		return
	}
	switch x := a.(type) {
	case map[string]any:
		y := b.(map[string]any)
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			va, ina := x[k]
			vb, inb := y[k]
			p := joinPath(path, k)
			switch {
			case !inb:
				*out = append(*out, semChange{p, "removed", va, nil})
			case !ina:
				*out = append(*out, semChange{p, "added", nil, vb})
			default:
				semDiff(p, va, vb, out)
			}
		}
	case []any:
		y := b.([]any)
		ida, idb := map[string]int{}, map[string]int{}
		keyed := len(x)+len(y) > 0
		for i, v := range x {
			id, ok := itemID(v)
			if !ok || ida[id] > 0 {
				keyed = false
				break
			}
			ida[id] = i + 1
		}
		for i, v := range y {
			id, ok := itemID(v)
			if !ok || idb[id] > 0 {
				keyed = false
				break
			}
			idb[id] = i + 1
		}
		if keyed {
			var ids []string
			for id := range ida {
				ids = append(ids, id)
			}
			for id := range idb {
				if ida[id] == 0 {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			for _, id := range ids {
				p := path + "[" + id + "]"
				switch {
				case idb[id] == 0:
					*out = append(*out, semChange{p, "removed", x[ida[id]-1], nil})
				case ida[id] == 0:
					*out = append(*out, semChange{p, "added", nil, y[idb[id]-1]})
				default:
					semDiff(p, x[ida[id]-1], y[idb[id]-1], out)
				}
			}
			return
		}
		for i := 0; i < max(len(x), len(y)); i++ {
			p := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(y):
				*out = append(*out, semChange{p, "removed", x[i], nil})
			case i >= len(x):
				*out = append(*out, semChange{p, "added", nil, y[i]})
			default:
				semDiff(p, x[i], y[i], out)
			}
		}
	default:
		if fmt.Sprint(a) != fmt.Sprint(b) {
			*out = append(*out, semChange{path, "changed", a, b})
		}
	}
}

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func printSemantic(pa, pb string, cs []semChange) {
	kv("A", pa)
	kv("B", pb)
	c := map[string]int{}
	for _, x := range cs {
		c[x.Kind]++
	}
	kv("Changes", fmt.Sprintf("%s · %s · %s · %s", col(fmt.Sprintf("changed %d", c["changed"]), YLW),
		col(fmt.Sprintf("added %d", c["added"]), GRN), col(fmt.Sprintf("removed %d", c["removed"]), R), col(fmt.Sprintf("type %d", c["type"]), OR)))
	fmt.Println()
	if len(cs) == 0 {
		okMsg("Semantically identical (key order, formatting and comments ignored).")
		return
	}
	for _, x := range cs {
		p := col(x.Path, W+B)
		switch x.Kind {
		case "changed":
			fmt.Println("  " + col("~ ", YLW+B) + p + col(": ", DIM) + col(trunc(compact(x.Old), 50), R) + col(" → ", DIM) + col(trunc(compact(x.New), 50), GRN))
		case "type":
			fmt.Println("  " + col("! ", OR+B) + p + col(": ", DIM) + col(trunc(compact(x.Old), 40), R) + col(" → ", DIM) + col(trunc(compact(x.New), 40), GRN) +
				col(fmt.Sprintf("  (%s → %s)", kindOf(x.Old), kindOf(x.New)), DIM))
		case "added":
			fmt.Println("  " + col("+ ", GRN+B) + p + col(": ", DIM) + col(trunc(compact(x.New), 90), GRN))
		case "removed":
			fmt.Println("  " + col("- ", R+B) + p + col(": ", DIM) + col(trunc(compact(x.Old), 90), R))
		}
	}
}

func semanticFiles(pa, pb string) ([]semChange, error) {
	a, err := loadFile(pa)
	if err != nil {
		return nil, err
	}
	b, err := loadFile(pb)
	if err != nil {
		return nil, err
	}
	va, err := parseData(pa, a.data)
	if err != nil {
		return nil, err
	}
	vb, err := parseData(pb, b.data)
	if err != nil {
		return nil, err
	}
	cs := []semChange{}
	semDiff("", va, vb, &cs)
	return cs, nil
}

func isDataFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".json", ".yaml", ".yml":
		return true
	}
	return false
}

// ====================================================================
//  REPORTS: JSON + HTML
// ====================================================================

type fileReport struct {
	A         string     `json:"a"`
	B         string     `json:"b"`
	ShaA      string     `json:"sha256_a"`
	ShaB      string     `json:"sha256_b"`
	Binary    bool       `json:"binary"`
	Identical bool       `json:"identical"`
	Options   string     `json:"options"`
	Stats     *diffStats `json:"stats,omitempty"`
	Hunks     []hunk     `json:"hunks,omitempty"`
	BinNote   string     `json:"binary_change,omitempty"`
}

type report struct {
	Tool      string         `json:"tool"`
	Version   string         `json:"version"`
	Generated string         `json:"generated"`
	Files     []fileReport   `json:"files,omitempty"`
	Dir       []dirResult    `json:"directory,omitempty"`
	DirCounts map[string]int `json:"directory_counts,omitempty"`
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func fileReportOf(a, b textFile) fileReport {
	fr := fileReport{A: a.name, B: b.name, ShaA: sha(a.data), ShaB: sha(b.data), Options: opt.String()}
	if a.binary || b.binary {
		fr.Binary = true
		fr.Identical = fr.ShaA == fr.ShaB
		fr.BinNote = diffBinary(a.data, b.data).describe()
		return fr
	}
	ed := diffLines(a, b)
	hs := buildHunks(a, b, ed, opt.ctx)
	st := statsOf(a, b, ed, hs)
	fr.Stats, fr.Hunks, fr.Identical = &st, hs, len(hs) == 0
	if fr.Hunks == nil {
		fr.Hunks = []hunk{}
	}
	return fr
}

func newReport() report {
	return report{Tool: APPNAME, Version: VERSION, Generated: time.Now().UTC().Format(time.RFC3339)}
}

func (r report) json() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b) + "\n"
}

// htmlReport: one self-contained side-by-side page (no external resources).
func (r report) html() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OFileDiff Report</title><style>
:root{--mv:#ece4fa;--bg:#f4f4f2;--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--line:#e2e1dc;--brand:#0e7c86;--del:#fde8e8;--delhl:#f5b5b5;--ins:#e3f6e3;--inshl:#a8e2a8;--hunk:#eef4fb}
@media (prefers-color-scheme:dark){:root{--mv:#2c2340;--bg:#111110;--surface:#1a1a19;--ink:#f0efec;--ink2:#c3c2b7;--line:#2e2e2b;--brand:#3fc1cc;--del:#3a1d1d;--delhl:#7a2c2c;--ins:#16301a;--inshl:#2c6a35;--hunk:#1d2733}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1400px;margin:0 auto;padding:24px 16px 48px}h1{margin:0;font-size:22px}h1 span{color:var(--brand)}
.meta{color:var(--ink2);font-size:13px;margin:6px 0 18px}.card{background:var(--surface);border:1px solid var(--line);border-radius:10px;margin-bottom:16px;overflow:hidden}
.card h2{font-size:14px;margin:0;padding:10px 14px;border-bottom:1px solid var(--line);display:flex;gap:12px;flex-wrap:wrap;align-items:baseline}
.card h2 small{color:var(--ink2);font-weight:400}.add{color:#1b8a1b}.rem{color:#c43131}
.wrap{overflow-x:auto}table{border-collapse:collapse;width:100%;font:12.5px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
td{padding:0 8px;vertical-align:top;white-space:pre-wrap;word-break:break-all}td.n{width:1%;color:var(--ink2);text-align:right;user-select:none;white-space:nowrap}
td.d{background:var(--del)}td.i{background:var(--ins)}td.mv{background:var(--mv)}td.d .w{background:var(--delhl)}td.i .w{background:var(--inshl)}
tr.h td{background:var(--hunk);color:var(--ink2);padding:2px 8px}td.sep{border-left:1px solid var(--line)}
.dir td:first-child{width:1%}.dir td{white-space:nowrap;font-family:system-ui,sans-serif;padding:4px 10px;border-bottom:1px solid var(--line)}
footer{color:var(--ink2);font-size:12px;margin-top:20px}
</style></head><body><main>`)
	fmt.Fprintf(&b, `<h1><span>OFileDiff</span> — diff report</h1><div class="meta">generated %s · %s</div>`, e(r.Generated), e(opt.String()))
	if len(r.Dir) > 0 {
		var cs []string
		for _, k := range []string{"changed", "only-a", "only-b", "type", "error", "same"} {
			cs = append(cs, fmt.Sprintf("%s %d", k, r.DirCounts[k]))
		}
		b.WriteString(`<div class="card"><h2>Directory compare<small>` + strings.Join(cs, " · ") + `</small></h2><div class="wrap"><table class="dir">`)
		for _, d := range visibleDiffs(r.Dir) {
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`, e(d.Status), e(d.Path), e(d.Note))
		}
		b.WriteString(`</table></div></div>`)
	}
	segHTML := func(ss []seg, raw string) string {
		if ss == nil {
			return e(raw)
		}
		var sb strings.Builder
		for _, s := range ss {
			if s.changed {
				sb.WriteString(`<span class="w">` + e(s.text) + `</span>`)
			} else {
				sb.WriteString(e(s.text))
			}
		}
		return sb.String()
	}
	for _, f := range r.Files {
		fmt.Fprintf(&b, `<div class="card"><h2>%s → %s`, e(f.A), e(f.B))
		switch {
		case f.Identical:
			b.WriteString(`<small>identical</small>`)
		case f.Binary:
			fmt.Fprintf(&b, `<small>binary: %s</small>`, e(f.BinNote))
		default:
			fmt.Fprintf(&b, `<small><span class="add">+%d</span> <span class="rem">−%d</span> · %d hunk(s) · %.1f%% similar</small>`,
				f.Stats.Added, f.Stats.Removed, f.Stats.Hunks, f.Stats.Similarity)
		}
		b.WriteString(`</h2>`)
		if len(f.Hunks) > 0 {
			b.WriteString(`<div class="wrap"><table>`)
			detectMoves(f.Hunks)
			for _, h := range f.Hunks {
				fmt.Fprintf(&b, `<tr class="h"><td colspan="4">%s</td></tr>`, e(h.header()))
				for _, row := range sideRows(h) {
					l, rr := "", ""
					if row.left != nil && row.left.Op == opEq {
						fmt.Fprintf(&b, `<tr><td class="n">%d</td><td>%s</td><td class="n sep">%d</td><td>%s</td></tr>`,
							row.left.ANo, e(row.left.Text), row.right.BNo, e(row.right.Text))
						continue
					}
					ln, rn := "", ""
					lc, rc := "", ""
					if row.left != nil {
						ln, l, lc = strconv.Itoa(row.left.ANo), segHTML(row.lsegs, row.left.Text), ` class="d"`
						if isMoved(row.left) {
							l, lc = e(row.left.Text), ` class="mv" title="moved"`
						}
					}
					if row.right != nil {
						rn, rr, rc = strconv.Itoa(row.right.BNo), segHTML(row.rsegs, row.right.Text), ` class="i"`
						if isMoved(row.right) {
							rr, rc = e(row.right.Text), ` class="mv" title="moved"`
						}
					}
					fmt.Fprintf(&b, `<tr><td class="n">%s</td><td%s>%s</td><td class="n sep">%s</td><td%s>%s</td></tr>`, ln, lc, l, rn, rc, rr)
				}
			}
			b.WriteString(`</table></div>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<footer>OFileDiff v` + VERSION + ` · Owl Core Suite · owlsec.org</footer></main></body></html>` + "\n")
	return b.String()
}

func writeOut(content, path string) error {
	if path == "" || path == "-" {
		fmt.Print(content)
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ====================================================================
//  DEMO + SELF-TEST
// ====================================================================

func demoPair() (textFile, textFile) {
	a := "# sshd_config (before)\nPort 22\nPermitRootLogin yes\nPasswordAuthentication yes\nX11Forwarding yes\n" +
		"MaxAuthTries 6\nClientAliveInterval 0\nSubsystem sftp /usr/lib/openssh/sftp-server\nUsePAM yes"
	b := "# sshd_config (hardened)\nPort 2222\nPermitRootLogin no\nPasswordAuthentication no\nPubkeyAuthentication yes\nX11Forwarding no\n" +
		"MaxAuthTries 3\nClientAliveInterval 300\nSubsystem sftp /usr/lib/openssh/sftp-server\nUsePAM yes\n"
	return newText("sshd_config.orig", []byte(a)), newText("sshd_config", []byte(b))
}

func runDemo() {
	sectionHeader("Demo — sshd_config hardening")
	a, b := demoPair()
	ed := diffLines(a, b)
	hs := buildHunks(a, b, ed, opt.ctx)
	printUnifiedOrdered(a, b, hs, 0)
	fmt.Println()
	st := statsOf(a, b, ed, hs)
	fmt.Println("  " + col(statLine("sshd_config", st.Added, st.Removed, 40, true), W))
	kv("Similarity", fmt.Sprintf("%.1f%%", st.Similarity))
	info("note the \"\\ No newline at end of file\" marker: the original lacked a final newline")
	fmt.Println()
	sectionHeader("Demo — side by side")
	printSideBySide(a, b, hs, min(termWidth(), 140))
}

// lcsLen is the O(nm) reference used to check that the diff is minimal.
func lcsLen(a, b []int) int {
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func runSelfTest() int {
	var rows [][]string
	fail, total := 0, 0
	check := func(name, expect, got string, ok bool) {
		total++
		res := col("PASS", GRN+B)
		if !ok {
			res = col("FAIL", R+B)
			fail++
		}
		rows = append(rows, []string{trunc(name, 38), trunc(expect, 24), trunc(got, 26), res})
	}
	saved := opt
	defer func() { opt = saved }()
	opt = options{ctx: 3}

	// Myers paper example: ABCABBA → CBABAC has D = 5
	toInts := func(s string) []int {
		var o []int
		for _, c := range s {
			o = append(o, int(c))
		}
		return o
	}
	ra, rb := diffInts(toInts("ABCABBA"), toInts("CBABAC"))
	d := 0
	for _, x := range ra {
		if x {
			d++
		}
	}
	for _, x := range rb {
		if x {
			d++
		}
	}
	check("Myers paper example (D=5)", "5", strconv.Itoa(d), d == 5)

	// random: minimal (vs LCS) and round-trip through our patch applier
	rng := rand.New(rand.NewSource(7))
	minimal, roundtrip := 0, 0
	const cases = 400
	for c := 0; c < cases; c++ {
		gen := func() textFile {
			n := rng.Intn(30)
			var ls []string
			for i := 0; i < n; i++ {
				ls = append(ls, string(rune('a'+rng.Intn(5))))
			}
			s := strings.Join(ls, "\n")
			if n > 0 && rng.Intn(3) > 0 {
				s += "\n"
			}
			return newText("x", []byte(s))
		}
		a, b := gen(), gen()
		ids := map[string]int{}
		conv := func(t textFile) []int {
			var o []int
			for i, l := range t.lines {
				if t.noEOL && i == len(t.lines)-1 {
					l += "\x00"
				}
				if _, ok := ids[l]; !ok {
					ids[l] = len(ids)
				}
				o = append(o, ids[l])
			}
			return o
		}
		ia, ib := conv(a), conv(b)
		xa, xb := diffInts(ia, ib)
		dd := 0
		for _, x := range xa {
			if x {
				dd++
			}
		}
		for _, x := range xb {
			if x {
				dd++
			}
		}
		if dd == len(ia)+len(ib)-2*lcsLen(ia, ib) {
			minimal++
		}
		opt.ctx = rng.Intn(4)
		hs := buildHunks(a, b, diffLines(a, b), opt.ctx)
		p := unifiedText(a, b, "a", "b", hs)
		got := a.data
		if p != "" {
			fps, err := parsePatch(p)
			if err == nil {
				if res, err := applyHunks(a, fps[0].hunks); err == nil {
					got = res.bytes()
				}
			}
		}
		if bytes.Equal(got, b.data) {
			roundtrip++
		}
	}
	opt.ctx = 3
	check("random diffs are minimal (vs LCS)", fmt.Sprintf("%d/%d", cases, cases), fmt.Sprintf("%d/%d", minimal, cases), minimal == cases)
	check("random patch round-trip (own applier)", fmt.Sprintf("%d/%d", cases, cases), fmt.Sprintf("%d/%d", roundtrip, cases), roundtrip == cases)

	// GNU conventions
	a := newText("a", []byte("x\ny\n"))
	b := newText("b", []byte("n1\nn2\nx\ny\n"))
	hs := buildHunks(a, b, diffLines(a, b), 0)
	check("insert at top header", "@@ -0,0 +1,2 @@", hs[0].header(), len(hs) == 1 && hs[0].header() == "@@ -0,0 +1,2 @@")
	a, b = newText("a", []byte("same\nlast")), newText("b", []byte("same\nlast\n"))
	p := unifiedText(a, b, "a", "b", buildHunks(a, b, diffLines(a, b), 3))
	check("missing final newline is a change", "-last \\ No newline / +last",
		strconv.Itoa(strings.Count(p, "No newline")), strings.Contains(p, "-last\n\\ No newline at end of file\n+last\n"))

	// options
	opt.ignoreAllWS = true
	a, b = newText("a", []byte("if (x==1) {\n")), newText("b", []byte("if ( x == 1 )  {\n"))
	hw := len(buildHunks(a, b, diffLines(a, b), 3))
	opt.ignoreAllWS, opt.ignoreCase = false, true
	a2, b2 := newText("a", []byte("Hello World\n")), newText("b", []byte("hello world\n"))
	hc := len(buildHunks(a2, b2, diffLines(a2, b2), 3))
	opt.ignoreCase, opt.ignoreBlank = false, true
	a3, b3 := newText("a", []byte("a\nb\n")), newText("b", []byte("a\n\n\nb\n"))
	hb := len(buildHunks(a3, b3, diffLines(a3, b3), 3))
	opt.ignoreBlank = false
	check("-w / -i / -B suppress hunks", "0 0 0", fmt.Sprint(hw, hc, hb), hw == 0 && hc == 0 && hb == 0)

	// word diff
	ls, rs := wordDiff("PermitRootLogin yes", "PermitRootLogin no")
	al := alignBlock([]hline{{Text: "X11Forwarding yes"}, {Text: "MaxAuthTries 6"}},
		[]hline{{Text: "PubkeyAuthentication yes"}, {Text: "X11Forwarding no"}, {Text: "MaxAuthTries 3"}})
	check("line alignment survives an inserted line", "[-1 0][0 1][1 2]", fmt.Sprint(al),
		fmt.Sprint(al) == "[[-1 0] [0 1] [1 2]]")
	check("word diff isolates the changed word", "yes → no", fmt.Sprint(ls, rs),
		len(ls) == 2 && ls[1].text == "yes" && ls[1].changed && !ls[0].changed && rs[1].text == "no")

	// applier: offsets, mismatch, traversal
	base := newText("t", []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n"))
	mod := newText("t", []byte("1\n2\n3\n4\nFIVE\n6\n7\n8\n9\n"))
	pp := unifiedText(base, mod, "t", "t", buildHunks(base, mod, diffLines(base, mod), 1))
	shifted := newText("t", []byte("0a\n0b\n1\n2\n3\n4\n5\n6\n7\n8\n9\n"))
	fps, _ := parsePatch(pp)
	res, err := applyHunks(shifted, fps[0].hunks)
	check("applier finds hunk at offset +2", "offset 2", fmt.Sprint(res.offsets, err), err == nil && len(res.offsets) == 1 && res.offsets[0] == 2 &&
		strings.Contains(string(res.bytes()), "4\nFIVE\n6"))
	_, err = applyHunks(newText("t", []byte("x\ny\n")), fps[0].hunks)
	check("applier rejects a non-matching hunk", "error", fmt.Sprint(err != nil), err != nil)
	dir, _ := os.MkdirTemp("", "ofilediff-")
	defer os.RemoveAll(dir)
	evil := "--- a/../../etc/passwd\n+++ b/../../etc/passwd\n@@ -1 +1 @@\n-root\n+owned\n"
	reps, _ := applyPatch(evil, dir, 1, false, true)
	check("path traversal in a patch is refused", "refused", fmt.Sprint(reps[0].err), len(reps) == 1 && reps[0].err != nil &&
		strings.Contains(reps[0].err.Error(), "escapes"))

	// binary
	bd := diffBinary([]byte("HEADERpayloadTRAILER"), []byte("HEADERpay1oadTRAILER"))
	bi := diffBinary([]byte("abcdef"), []byte("abcXYZdef"))
	check("binary: in-place patch vs insertion", "1 byte / 3 inserted", bd.describe()+" | "+bi.describe(),
		bd.DiffBytes == 1 && strings.HasPrefix(bi.describe(), "3 byte(s) inserted at 0x3"))
	check("binary detection (NUL)", "true false", fmt.Sprint(isBinary([]byte("ab\x00c")), isBinary([]byte("abc"))),
		isBinary([]byte("ab\x00c")) && !isBinary([]byte("abc")))

	// directory compare + -ruN patch round trip
	da, db := filepath.Join(dir, "A"), filepath.Join(dir, "B")
	for _, p := range []string{"A/sub", "B/sub", "A/gone"} {
		os.MkdirAll(filepath.Join(dir, p), 0o755)
	}
	os.WriteFile(filepath.Join(da, "same.txt"), []byte("same\n"), 0o644)
	os.WriteFile(filepath.Join(db, "same.txt"), []byte("same\n"), 0o644)
	os.WriteFile(filepath.Join(da, "sub/conf"), []byte("k=1\nx=2\n"), 0o644)
	os.WriteFile(filepath.Join(db, "sub/conf"), []byte("k=1\nx=3\n"), 0o644)
	os.WriteFile(filepath.Join(da, "gone/old.txt"), []byte("bye\n"), 0o644)
	os.WriteFile(filepath.Join(db, "new.txt"), []byte("hi\n"), 0o644)
	os.WriteFile(filepath.Join(da, "kind"), []byte("file\n"), 0o644)
	os.MkdirAll(filepath.Join(db, "kind"), 0o755)
	drs, _ := compareDirs(da, db)
	c := dirCounts(drs)
	check("dir compare statuses", "chg1 A1 B1 type1 same1", fmt.Sprint(c), c["changed"] == 1 && c["only-a"] == 1 &&
		c["only-b"] == 1 && c["type"] == 1 && c["same"] == 1)
	dp := dirPatch(da, db, drs)
	work := filepath.Join(dir, "W")
	cp := exec.Command("cp", "-r", da, work)
	if cp.Run() == nil {
		os.Remove(filepath.Join(work, "kind"))
		reps, err := applyPatch(dp, work, 1, false, false)
		ok := err == nil
		for _, r := range reps {
			if r.err != nil {
				ok = false
			}
		}
		got, _ := os.ReadFile(filepath.Join(work, "sub/conf"))
		_, gone := os.Stat(filepath.Join(work, "gone/old.txt"))
		nw, _ := os.ReadFile(filepath.Join(work, "new.txt"))
		check("dir -ruN patch applies with -p1", "conf, new, deleted", fmt.Sprint(ok, len(reps)),
			ok && string(got) == "k=1\nx=3\n" && os.IsNotExist(gone) && string(nw) == "hi\n")
	}

	// GNU patch accepts our output (including no-newline markers)
	if bin, err := exec.LookPath("patch"); err == nil {
		agree, n := 0, 0
		for c := 0; c < 60; c++ {
			gen := func() []byte {
				var ls []string
				for i := rng.Intn(20) + 1; i > 0; i-- {
					ls = append(ls, string(rune('a'+rng.Intn(4)))+strings.Repeat("x", rng.Intn(3)))
				}
				s := strings.Join(ls, "\n")
				if rng.Intn(2) == 0 {
					s += "\n"
				}
				return []byte(s)
			}
			x, y := gen(), gen()
			ta, tb := newText("f", x), newText("f", y)
			hs := buildHunks(ta, tb, diffLines(ta, tb), 3)
			if len(hs) == 0 {
				continue
			}
			n++
			f := filepath.Join(dir, "gnu.txt")
			os.WriteFile(f, x, 0o644)
			pt := unifiedText(ta, tb, "gnu.txt", "gnu.txt", hs)
			cmd := exec.Command(bin, "-s", "-N", f)
			cmd.Stdin = strings.NewReader(pt)
			if cmd.Run() == nil {
				if out, _ := os.ReadFile(f); bytes.Equal(out, y) {
					agree++
				}
			}
		}
		check("GNU patch applies our output", fmt.Sprintf("%d/%d", n, n), fmt.Sprintf("%d/%d", agree, n), agree == n)
	}

	// big input stays fast and small (linear space)
	var bigA, bigB strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&bigA, "line %d\n", i)
		if i%7 == 0 {
			fmt.Fprintf(&bigB, "changed %d\n", i)
		} else {
			fmt.Fprintf(&bigB, "line %d\n", i)
		}
	}
	t0 := time.Now()
	ta, tb := newText("a", []byte(bigA.String())), newText("b", []byte(bigB.String()))
	hs = buildHunks(ta, tb, diffLines(ta, tb), 3)
	el := time.Since(t0)
	check("20k lines, 2858 changes", "< 5s", el.Round(time.Millisecond).String(), el < 5*time.Second && statsOf(ta, tb, diffLines(ta, tb), hs).Added == 2858)

	selfTestExtras(check, rng)
	table([]string{"CASE", "EXPECT", "GOT", "RESULT"}, rows)
	fmt.Println()
	if fail == 0 {
		okMsg(fmt.Sprintf("self-test: %d/%d passed", total, total))
	} else {
		errMsg(fmt.Sprintf("self-test: %d/%d FAILED", fail, total))
	}
	return fail
}

// selfTestExtras covers patience, moved blocks, 3-way merge, snapshots and
// the semantic JSON / YAML diff.
func selfTestExtras(check func(name, expect, got string, ok bool), rng *rand.Rand) {
	saved := opt
	defer func() { opt = saved }()

	// patience: round-trip on random input + groups a new function cleanly
	opt = options{ctx: 3, patience: true, colorMoved: true}
	rt := 0
	for c := 0; c < 300; c++ {
		gen := func() textFile {
			var ls []string
			for i := rng.Intn(25); i > 0; i-- {
				ls = append(ls, string(rune('a'+rng.Intn(6))))
			}
			return newText("x", []byte(strings.Join(ls, "\n")+"\n"))
		}
		a, b := gen(), gen()
		p := unifiedText(a, b, "a", "b", buildHunks(a, b, diffLines(a, b), 2))
		got := a.data
		if fps, err := parsePatch(p); p != "" && err == nil {
			if res, err := applyHunks(a, fps[0].hunks); err == nil {
				got = res.bytes()
			}
		}
		if bytes.Equal(got, b.data) || len(a.lines) == 0 && len(b.lines) == 0 {
			rt++
		}
	}
	check("patience diffs round-trip", "300/300", fmt.Sprintf("%d/300", rt), rt == 300)
	src := "func a() {\n\tx()\n}\n\nfunc b() {\n\ty()\n}\n"
	dst := "func a() {\n\tx()\n}\n\nfunc c() {\n\tz()\n}\n\nfunc b() {\n\ty()\n}\n"
	ta, tb := newText("a", []byte(src)), newText("b", []byte(dst))
	first := ""
	for _, l := range buildHunks(ta, tb, diffLines(ta, tb), 0)[0].Lines {
		if l.Op == opIns {
			first = l.Text
			break
		}
	}
	check("patience: inserted function starts at its header", "func c() {", first, first == "func c() {")

	// moved block
	opt = options{ctx: 3, colorMoved: true}
	mainFn := "def main():\n    args = parse()\n    cfg = load(args)\n    run(cfg)\n    report()\n    return 0\n"
	helper := "def helper(x):\n    return x * 2\n    # doubled\n"
	ma := newText("a", []byte("import os\n\n"+helper+"\n"+mainFn))
	mb := newText("b", []byte("import os\n\n"+mainFn+"\n"+helper))
	detectMoves(buildHunks(ma, mb, diffLines(ma, mb), 3))
	check("moved block detected", "≥1 block, ≥3 lines", fmt.Sprintf("%d blocks, %d lines", movedBlocks, movedLines), movedBlocks >= 1 && movedLines >= 3)

	// 3-way merge
	base := newText("base", []byte("host=a\nport=1\nuser=x\nmode=r\n"))
	mine := newText("A", []byte("host=b\nport=1\nuser=x\nmode=r\n"))
	theirs := newText("B", []byte("host=a\nport=1\nuser=x\nmode=rw\n"))
	r := merge3(base, mine, theirs, "A", "B", false)
	check("merge: edits in different places", "host=b … mode=rw, 0 conflicts", fmt.Sprint(r.conflicts),
		r.conflicts == 0 && string(r.bytes()) == "host=b\nport=1\nuser=x\nmode=rw\n")
	theirs2 := newText("B", []byte("host=c\nport=1\nuser=x\nmode=r\n"))
	r = merge3(base, mine, theirs2, "A", "B", true)
	check("merge: same line changed differently", "1 conflict + markers", fmt.Sprint(r.conflicts),
		r.conflicts == 1 && strings.Contains(string(r.bytes()), "<<<<<<< A\nhost=b\n||||||| base\nhost=a\n=======\nhost=c\n>>>>>>> B\n"))
	r = merge3(base, mine, mine, "A", "B", false)
	check("merge: identical edits on both sides", "clean", fmt.Sprint(r.conflicts), r.conflicts == 0 && string(r.bytes()) == string(mine.data))
	prop := 0
	for c := 0; c < 200; c++ {
		gen := func() textFile {
			var ls []string
			for i := rng.Intn(15); i > 0; i-- {
				ls = append(ls, string(rune('a'+rng.Intn(5))))
			}
			if len(ls) == 0 {
				return newText("x", nil)
			}
			return newText("x", []byte(strings.Join(ls, "\n")+"\n"))
		}
		b0, a1 := gen(), gen()
		r1 := merge3(b0, a1, b0, "A", "B", false)
		r2 := merge3(b0, b0, a1, "A", "B", false)
		if bytes.Equal(r1.bytes(), a1.data) && bytes.Equal(r2.bytes(), a1.data) && r1.conflicts+r2.conflicts == 0 {
			prop++
		}
	}
	check("merge(base, X, base) == X (random)", "200/200", fmt.Sprintf("%d/200", prop), prop == 200)

	// snapshot / verify
	dir, _ := os.MkdirTemp("", "ofd-snap-")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "keep"), []byte("same"), 0o644)
	os.WriteFile(filepath.Join(dir, "edit"), []byte("v1"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "perm"), []byte("x"), 0o644)
	m, _, err := snapshot(dir)
	os.WriteFile(filepath.Join(dir, "edit"), []byte("v2"), 0o644)
	os.Remove(filepath.Join(dir, "gone"))
	os.WriteFile(filepath.Join(dir, "new"), []byte("n"), 0o644)
	os.Chmod(filepath.Join(dir, "perm"), 0o755)
	os.Chtimes(filepath.Join(dir, "keep"), time.Now(), time.Now().Add(time.Hour)) // touched only
	rows, same, err2 := verifyManifest(m, dir)
	st := map[string]string{}
	for _, x := range rows {
		st[x.Path] = x.Status
	}
	check("snapshot → verify", "edit/new/gone/perm, keep same", fmt.Sprint(st, same, err, err2),
		err == nil && err2 == nil && st["edit"] == "modified" && st["new"] == "added" && st["gone"] == "removed" && st["perm"] == "mode" && same == 1)

	// semantic
	js := []byte(`{"spec":{"replicas":2,"containers":[{"name":"web","image":"nginx:1.25","ports":[80]},{"name":"log","image":"fluent:2"}]},"kind":"Deployment"}`)
	ym := []byte("# same deployment, other order, as YAML\nkind: Deployment\nspec:\n  containers:\n    - name: log\n      image: \"fluent:2\"\n    - image: nginx:1.25\n      name: web\n      ports: [80]\n  replicas: 2\n")
	va, e1 := parseData("a.json", js)
	vb, e2 := parseData("b.yaml", ym)
	var cs []semChange
	semDiff("", va, vb, &cs)
	check("JSON ≡ reordered YAML", "0 changes", fmt.Sprint(len(cs), e1, e2), e1 == nil && e2 == nil && len(cs) == 0)
	ym2 := strings.Replace(string(ym), "nginx:1.25", "nginx:1.27", 1)
	ym2 = strings.Replace(ym2, "replicas: 2", "replicas: 3", 1)
	vb, _ = parseData("b.yaml", []byte(ym2))
	cs = nil
	semDiff("", va, vb, &cs)
	var paths []string
	for _, c := range cs {
		paths = append(paths, c.Path)
	}
	check("list items matched by name", "image + replicas", strings.Join(paths, " "), len(cs) == 2 &&
		paths[0] == "spec.containers[name=web].image" && paths[1] == "spec.replicas")
	yv, err := parseYAML("a: |\n  line1\n  line2\nb: {x: 1, y: [true, 'q']}\nc: \"x # not a comment\" # comment\nd: ~\n")
	mm, _ := yv.(map[string]any)
	ok := err == nil && mm["a"] == "line1\nline2\n" && mm["c"] == "x # not a comment" && mm["d"] == nil
	if bm, _ := mm["b"].(map[string]any); bm == nil || fmt.Sprint(bm["y"]) != "[true q]" {
		ok = false
	}
	check("YAML: block scalar, flow, comments, null", "parsed", fmt.Sprint(err), ok)
}

// ====================================================================
//  INTERACTIVE TUI
// ====================================================================

type tuiAction struct {
	name, desc string
	fn         func()
}

var tuiActions []tuiAction

func init() {
	tuiActions = []tuiAction{
		{"Text diff", "coloured unified view + word highlight", uiTextDiff},
		{"Side-by-side view", "two columns with line numbers", uiSideBySide},
		{"Write unified patch", "file or directory → .patch", uiWritePatch},
		{"Apply a patch", "dry run · apply · reverse · -p N", uiApply},
		{"Diff statistics", "diffstat · similarity · hunks", uiStats},
		{"Binary diff", "alignment · ranges · hex view", uiBinary},
		{"Directory compare", "changed · only-A/B · type changes", uiDirCompare},
		{"Semantic diff", "JSON / YAML compared as data", uiSemantic},
		{"3-way merge", "base + two edits → merged + conflicts", uiMerge},
		{"Snapshot a directory", "SHA-256 integrity manifest", uiSnapshot},
		{"Verify a snapshot", "what changed since the manifest", uiVerify},
		{"HTML report", "self-contained side-by-side page", uiHTML},
		{"Options", "context · whitespace · case · blank lines", uiOptions},
		{"Run demo", "sshd_config hardening diff", runDemo},
		{"Self-test", "minimality · round-trip · GNU patch", func() { sectionHeader("Self-Test"); runSelfTest() }},
	}
}

func mainMenu() {
	for {
		clear()
		banner()
		fmt.Println("  " + col("⬡  Main Menu", B+CY) + col("     "+opt.String(), DIM))
		fmt.Println()
		for i, a := range tuiActions {
			fmt.Println("  " + col(pad(fmt.Sprintf("[%d]", i+1), 4), CY+B) + "  " +
				col(pad(a.name, 26), W) + col("─ "+a.desc, DIM))
		}
		fmt.Println()
		fmt.Println("  " + col(pad("[H]", 4), YLW+B) + "  " + col(pad("Help & Usage", 26), W) + col("─ How to use, every option, CLI flags & examples", DIM))
		fmt.Println("  " + col(pad("[0]", 4), R+B) + "  " + col(pad("Exit", 26), W) + col("─ Quit "+APPNAME, DIM))
		fmt.Println()
		rule()
		c := strings.TrimLeft(prompt("Select option"), "0")
		if c == "" {
			clear()
			okMsg("Goodbye.")
			return
		}
		if strings.EqualFold(c, "h") || strings.EqualFold(c, "help") || c == "?" {
			tuiHelp()
			continue
		}
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > len(tuiActions) {
			errMsg("Unknown option")
			pause()
			continue
		}
		clear()
		banner()
		tuiActions[n-1].fn()
		pause()
	}
}

func askPair() (textFile, textFile, bool) {
	pa := prompt("file A (original)")
	a, err := loadFile(pa)
	if err != nil {
		errMsg(err.Error())
		return textFile{}, textFile{}, false
	}
	pb := prompt("file B (modified)")
	b, err := loadFile(pb)
	if err != nil {
		errMsg(err.Error())
		return textFile{}, textFile{}, false
	}
	return a, b, true
}

func uiTextDiff() {
	sectionHeader("Text Diff")
	a, b, ok := askPair()
	if !ok {
		return
	}
	fmt.Println()
	if a.binary || b.binary {
		warnMsg("binary content — showing the binary diff")
		printBinary(a, b, 4)
		return
	}
	hs := buildHunks(a, b, diffLines(a, b), opt.ctx)
	if len(hs) == 0 {
		okMsg("Files are identical (" + opt.String() + ")")
		return
	}
	printUnifiedOrdered(a, b, hs, 400)
}

func uiSideBySide() {
	sectionHeader("Side-by-Side View")
	a, b, ok := askPair()
	if !ok {
		return
	}
	if a.binary || b.binary {
		warnMsg("binary content — use the binary diff")
		return
	}
	hs := buildHunks(a, b, diffLines(a, b), opt.ctx)
	if len(hs) == 0 {
		okMsg("Files are identical")
		return
	}
	fmt.Println()
	printSideBySide(a, b, hs, termWidth())
}

func uiWritePatch() {
	sectionHeader("Write Unified Patch")
	pa, pb := prompt("A (file or directory)"), prompt("B (file or directory)")
	out := prompt("output patch file (blank = out.patch)")
	if out == "" {
		out = "out.patch"
	}
	text, err := patchFor(pa, pb)
	if err != nil {
		errMsg(err.Error())
		return
	}
	if text == "" {
		okMsg("No differences — no patch written")
		return
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		errMsg(err.Error())
		return
	}
	okMsg(fmt.Sprintf("wrote %s (%d lines)", out, strings.Count(text, "\n")))
	if fi, err := os.Stat(pa); err == nil && fi.IsDir() {
		info("apply inside a copy of A with: OFileDiff --apply " + out + " -p 1   (or: patch -p1 < " + out + ")")
	} else {
		info("apply with: OFileDiff --apply " + out + "   (or: patch -p0 < " + out + ")")
	}
}

// patchFor builds a unified patch for two files or two directories.
func patchFor(pa, pb string) (string, error) {
	fa, errA := os.Stat(pa)
	fb, errB := os.Stat(pb)
	if errA != nil {
		return "", errA
	}
	if errB != nil {
		return "", errB
	}
	if fa.IsDir() && fb.IsDir() {
		rs, _ := compareDirs(pa, pb)
		return dirPatch(pa, pb, rs), nil
	}
	a, err := loadFile(pa)
	if err != nil {
		return "", err
	}
	b, err := loadFile(pb)
	if err != nil {
		return "", err
	}
	if a.binary || b.binary {
		if bytes.Equal(a.data, b.data) {
			return "", nil
		}
		return fmt.Sprintf("Binary files %s and %s differ\n", pa, pb), nil
	}
	return unifiedText(a, b, "", "", buildHunks(a, b, diffLines(a, b), opt.ctx)), nil
}

func uiApply() {
	sectionHeader("Apply a Patch")
	p := prompt("patch file")
	data, err := os.ReadFile(p)
	if err != nil {
		errMsg(err.Error())
		return
	}
	dir := prompt("apply inside directory (blank = .)")
	if dir == "" {
		dir = "."
	}
	strip, _ := strconv.Atoi(prompt("strip leading path components -p (blank = 0)"))
	rev := strings.HasPrefix(strings.ToLower(prompt("reverse (undo) the patch? [y/N]")), "y")
	reps, err := applyPatch(string(data), dir, strip, rev, true)
	if err != nil {
		errMsg(err.Error())
		return
	}
	fmt.Println()
	if printApply(reps, true) > 0 {
		warnMsg("dry run failed — nothing changed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(prompt("dry run clean — apply for real? [y/N]")), "y") {
		info("nothing changed")
		return
	}
	reps, _ = applyPatch(string(data), dir, strip, rev, false)
	printApply(reps, false)
}

func uiStats() {
	sectionHeader("Diff Statistics")
	a, b, ok := askPair()
	if !ok {
		return
	}
	if a.binary || b.binary {
		printBinary(a, b, 2)
		return
	}
	ed := diffLines(a, b)
	hs := buildHunks(a, b, ed, opt.ctx)
	st := statsOf(a, b, ed, hs)
	fmt.Println()
	table([]string{"METRIC", "VALUE"}, [][]string{
		{"lines in A", strconv.Itoa(st.ALines)},
		{"lines in B", strconv.Itoa(st.BLines)},
		{"unchanged", strconv.Itoa(st.Unchanged)},
		{"removed (-)", col(strconv.Itoa(st.Removed), R)},
		{"added (+)", col(strconv.Itoa(st.Added), GRN)},
		{"hunks", strconv.Itoa(st.Hunks)},
		{"edit distance", strconv.Itoa(st.Added + st.Removed)},
		{"similarity", fmt.Sprintf("%.2f%%", st.Similarity)},
	})
	fmt.Println()
	fmt.Println("  " + statLine(filepath.Base(b.name), st.Added, st.Removed, 40, true))
}

func uiBinary() {
	sectionHeader("Binary Diff")
	a, b, ok := askPair()
	if !ok {
		return
	}
	fmt.Println()
	printBinary(a, b, 8)
}

func printDir(ra, rb string, rs []dirResult, warns []string, limit int) {
	c := dirCounts(rs)
	kv("A", ra)
	kv("B", rb)
	var cs []string
	for _, k := range []string{"changed", "only-a", "only-b", "type", "error", "same"} {
		cs = append(cs, col(fmt.Sprintf("%s %d", k, c[k]), statusColor[k]))
	}
	kv("Result", strings.Join(cs, col(" · ", DIM)))
	for _, w := range warns {
		warnMsg(w)
	}
	fmt.Println()
	var tr [][]string
	for _, r := range visibleDiffs(rs) {
		size := ""
		switch r.Status {
		case "changed":
			if r.SizeA != r.SizeB {
				size = fmt.Sprintf("%d → %d B", r.SizeA, r.SizeB)
			} else {
				size = fmt.Sprintf("%d B", r.SizeA)
			}
		case "only-a":
			size = fmt.Sprintf("%d B", r.SizeA)
		case "only-b":
			size = fmt.Sprintf("%d B", r.SizeB)
		}
		if r.Note == "dir" {
			size = "directory"
		} else if r.Note != "" {
			size = r.Note
		}
		tr = append(tr, []string{col(strings.ToUpper(r.Status), statusColor[r.Status]), trunc(r.Path, 60), trunc(size, 30)})
	}
	if len(tr) == 0 {
		okMsg("Directories are identical")
		return
	}
	if limit > 0 && len(tr) > limit {
		tr = tr[:limit]
		defer info(fmt.Sprintf("showing the first %d differences (export for all)", limit))
	}
	table([]string{"STATUS", "PATH", "DETAIL"}, tr)
}

func uiDirCompare() {
	sectionHeader("Directory Compare")
	ra, rb := prompt("directory A"), prompt("directory B")
	if g := prompt("exclude globs, space separated (blank = none)"); g != "" {
		excludes = strings.Fields(g)
	}
	t := time.Now()
	rs, warns := compareDirs(ra, rb)
	fmt.Println()
	printDir(ra, rb, rs, warns, 80)
	info(fmt.Sprintf("compared in %s (size check first, then streamed byte compare)", time.Since(t).Round(time.Millisecond)))
}

func uiSemantic() {
	sectionHeader("Semantic Diff")
	pa, pb := prompt("A (.json / .yaml)"), prompt("B (.json / .yaml)")
	cs, err := semanticFiles(pa, pb)
	if err != nil {
		errMsg(err.Error())
		return
	}
	fmt.Println()
	printSemantic(pa, pb, cs)
}

func uiMerge() {
	sectionHeader("3-Way Merge")
	ps := []string{prompt("base (common ancestor)"), prompt("A (yours)"), prompt("B (theirs)")}
	var fs []textFile
	for _, p := range ps {
		t, err := loadFile(p)
		if err != nil {
			errMsg(err.Error())
			return
		}
		fs = append(fs, t)
	}
	r := merge3(fs[0], fs[1], fs[2], ps[1], ps[2], strings.HasPrefix(strings.ToLower(prompt("show base in conflicts (diff3 style)? [y/N]")), "y"))
	fmt.Println()
	kv("Auto-merged", strconv.Itoa(r.clean)+" chunk(s)")
	kv("Conflicts", strconv.Itoa(r.conflicts))
	fmt.Println()
	printMerge(r, 300)
	if out := prompt("save merged result to (blank = don't)"); out != "" {
		if err := os.WriteFile(out, r.bytes(), 0o644); err != nil {
			errMsg(err.Error())
		} else {
			okMsg("wrote " + out)
		}
	}
}

func uiSnapshot() {
	sectionHeader("Snapshot a Directory")
	d := prompt("directory")
	out := prompt("manifest file (blank = ofilediff-manifest.json)")
	if out == "" {
		out = "ofilediff-manifest.json"
	}
	t := time.Now()
	m, warns, err := snapshot(d)
	if err != nil {
		errMsg(err.Error())
		return
	}
	for _, w := range warns {
		warnMsg(w)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		errMsg(err.Error())
		return
	}
	okMsg(fmt.Sprintf("%d entries hashed in %s → %s", len(m.Files), time.Since(t).Round(time.Millisecond), out))
	info("keep the manifest somewhere the monitored system cannot write to")
}

func uiVerify() {
	sectionHeader("Verify a Snapshot")
	m, err := loadManifest(prompt("manifest file"))
	if err != nil {
		errMsg(err.Error())
		return
	}
	root := prompt("directory (blank = " + m.Root + ")")
	if root == "" {
		root = m.Root
	}
	rows, same, err := verifyManifest(m, root)
	if err != nil {
		errMsg(err.Error())
		return
	}
	fmt.Println()
	printVerify(m, root, rows, same)
}

func uiHTML() {
	sectionHeader("HTML Report")
	pa, pb := prompt("A (file or directory)"), prompt("B (file or directory)")
	out := prompt("output (blank = ofilediff-report.html)")
	if out == "" {
		out = "ofilediff-report.html"
	}
	r, err := buildReportFor(pa, pb)
	if err != nil {
		errMsg(err.Error())
		return
	}
	if err := writeOut(r.html(), out); err != nil {
		errMsg(err.Error())
		return
	}
	okMsg("wrote " + out)
}

func uiOptions() {
	sectionHeader("Options")
	kv("Current", opt.String())
	fmt.Println()
	if v := prompt(fmt.Sprintf("context lines (blank = %d)", opt.ctx)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opt.ctx = n
		}
	}
	yes := func(q string, cur bool) bool {
		v := strings.ToLower(prompt(fmt.Sprintf("%s [y/n] (blank = %v)", q, map[bool]string{true: "y", false: "n"}[cur])))
		switch v {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		return cur
	}
	opt.ignoreAllWS = yes("ignore all whitespace (-w)", opt.ignoreAllWS)
	if !opt.ignoreAllWS {
		opt.ignoreWSAmt = yes("ignore changes in amount of whitespace (-b)", opt.ignoreWSAmt)
	}
	opt.ignoreCase = yes("ignore case (-i)", opt.ignoreCase)
	opt.ignoreBlank = yes("ignore blank-line-only changes (-B)", opt.ignoreBlank)
	opt.stripCR = yes("strip trailing CR (CRLF vs LF)", opt.stripCR)
	opt.patience = yes("patience algorithm (cleaner code diffs)", opt.patience)
	opt.colorMoved = yes("colour moved blocks", opt.colorMoved)
	okMsg(opt.String())
}

// buildReportFor diffs two files or two directories into a report.
func buildReportFor(pa, pb string) (report, error) {
	r := newReport()
	fa, err := os.Stat(pa)
	if err != nil {
		return r, err
	}
	fb, err := os.Stat(pb)
	if err != nil {
		return r, err
	}
	if fa.IsDir() != fb.IsDir() {
		return r, errors.New("compare a file with a file, or a directory with a directory")
	}
	if fa.IsDir() {
		rs, _ := compareDirs(pa, pb)
		r.Dir, r.DirCounts = rs, dirCounts(rs)
		for _, d := range rs {
			if d.Status == "changed" && d.Note == "" {
				a, e1 := loadFile(filepath.Join(pa, d.Path))
				b, e2 := loadFile(filepath.Join(pb, d.Path))
				if e1 == nil && e2 == nil {
					fr := fileReportOf(a, b)
					fr.A, fr.B = "a/"+d.Path, "b/"+d.Path
					r.Files = append(r.Files, fr)
				}
			}
		}
		return r, nil
	}
	a, err := loadFile(pa)
	if err != nil {
		return r, err
	}
	b, err := loadFile(pb)
	if err != nil {
		return r, err
	}
	r.Files = []fileReport{fileReportOf(a, b)}
	return r, nil
}

// ====================================================================
//  CLI
// ====================================================================

func usage() {
	banner()
	fmt.Print(`USAGE:
  OFileDiff [flags] A B            compare two files or two directories
  OFileDiff --apply PATCH [flags]  apply a unified patch
  With no arguments the interactive TUI starts. "-" reads A or B from stdin.

COMPARE:
  -u, --context N        Lines of context (default 3)
  -w, --ignore-all-space Ignore all whitespace
  -b, --ignore-space-change  Ignore changes in the amount of whitespace
  -i, --ignore-case      Ignore case
  -B, --ignore-blank-lines   Ignore changes that only add / remove blank lines
  --strip-trailing-cr    Treat CRLF and LF line endings as equal
  --patience             Patience algorithm: anchors on unique lines (cleaner code diffs)
  --no-moved             Do not colour moved blocks (default: moved lines are magenta / cyan)
  --semantic             Compare JSON / YAML as data: key paths, order-insensitive,
                         list items matched by name / id
  -q, --brief            Only report whether the inputs differ
  --exclude GLOB         Directory mode: skip names / paths matching GLOB (repeatable)

OUTPUT (default: coloured diff on a terminal, plain unified patch when piped):
  -y, --side-by-side     Two-column view (--width N, default $COLUMNS or 160)
  --stat                 diffstat summary
  --format FMT           unified | json | html
  --output FILE          Write the result to FILE

PATCH:
  --apply FILE           Apply a unified / -ruN patch (refuses paths outside --dir)
  -p N                   Strip N leading path components (like patch -pN)
  -R, --reverse          Undo the patch
  --dry-run              Check that every hunk applies, change nothing
  --dir DIR              Directory to apply in (default .)

MERGE:
  --merge BASE A B       3-way merge; conflicts get <<<<<<< ======= >>>>>>> markers
  --diff3                Also show the base text inside conflicts (||||||| base)

INTEGRITY:
  --snapshot DIR         Write a SHA-256 manifest of DIR (to --output or stdout)
  --verify MANIFEST [DIR]  Report files modified / added / removed / re-permissioned since

OTHER:
  --demo                 sshd_config hardening example
  --selftest             Minimality, round-trip and GNU-patch compatibility tests
  --no-color             Disable colours
  --version              Print version

EXIT CODES (like diff):
  0 identical / patch applied · 1 differences found · 2 trouble

EXAMPLES:
  OFileDiff old.conf new.conf
  OFileDiff -w -y app_v1.py app_v2.py
  OFileDiff -r src-1.0 src-1.1 > upgrade.patch        # directories → -ruN patch
  OFileDiff --apply upgrade.patch -p 1 --dir src-1.0 --dry-run
  OFileDiff --format html --output diff.html release-a release-b
  OFileDiff --semantic deploy-old.yaml deploy-new.yaml
  OFileDiff --merge base.conf mine.conf theirs.conf --output merged.conf
  OFileDiff --snapshot /etc --output etc.manifest.json   # later: OFileDiff --verify etc.manifest.json
`)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// parseInterleaved lets flags appear before or after positional paths.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet(APPNAME, flag.ContinueOnError)
	fs.Usage = usage
	var ex multiFlag
	fs.IntVar(&opt.ctx, "u", 3, "")
	fs.IntVar(&opt.ctx, "context", 3, "")
	fs.BoolVar(&opt.ignoreAllWS, "w", false, "")
	fs.BoolVar(&opt.ignoreAllWS, "ignore-all-space", false, "")
	fs.BoolVar(&opt.ignoreWSAmt, "b", false, "")
	fs.BoolVar(&opt.ignoreWSAmt, "ignore-space-change", false, "")
	fs.BoolVar(&opt.ignoreCase, "i", false, "")
	fs.BoolVar(&opt.ignoreCase, "ignore-case", false, "")
	fs.BoolVar(&opt.ignoreBlank, "B", false, "")
	fs.BoolVar(&opt.ignoreBlank, "ignore-blank-lines", false, "")
	fs.BoolVar(&opt.stripCR, "strip-trailing-cr", false, "")
	var brief, sbs, stat, rev, dry, demo, self, nocol, ver, recursive bool
	fs.BoolVar(&brief, "q", false, "")
	fs.BoolVar(&brief, "brief", false, "")
	fs.BoolVar(&sbs, "y", false, "")
	fs.BoolVar(&sbs, "side-by-side", false, "")
	fs.BoolVar(&stat, "stat", false, "")
	fs.BoolVar(&rev, "R", false, "")
	fs.BoolVar(&rev, "reverse", false, "")
	fs.BoolVar(&dry, "dry-run", false, "")
	fs.BoolVar(&demo, "demo", false, "")
	fs.BoolVar(&self, "selftest", false, "")
	fs.BoolVar(&nocol, "no-color", false, "")
	fs.BoolVar(&ver, "version", false, "")
	fs.BoolVar(&recursive, "r", false, "") // accepted for diff muscle memory; directories are always recursive
	width := fs.Int("width", termWidth(), "")
	format := fs.String("format", "", "")
	output := fs.String("output", "", "")
	apply := fs.String("apply", "", "")
	strip := fs.Int("p", 0, "")
	dir := fs.String("dir", ".", "")
	fs.Var(&ex, "exclude", "")
	fs.BoolVar(&opt.patience, "patience", false, "")
	noMoved := fs.Bool("no-moved", false, "")
	mergeBase := fs.String("merge", "", "")
	diff3 := fs.Bool("diff3", false, "")
	snap := fs.String("snapshot", "", "")
	verify := fs.String("verify", "", "")
	semantic := fs.Bool("semantic", false, "")
	args, err := parseInterleaved(fs, os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	excludes = ex
	opt.colorMoved = !*noMoved
	if nocol || *output != "" {
		noColor = noColor || nocol
	}
	switch {
	case ver:
		fmt.Printf("%s v%s — %s\n", APPNAME, VERSION, TAGLINE)
		return 0
	case self:
		if runSelfTest() != 0 {
			return 2
		}
		return 0
	case demo:
		banner()
		runDemo()
		return 0
	case *apply != "":
		data, err := os.ReadFile(*apply)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		reps, err := applyPatch(string(data), *dir, *strip, rev, dry)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		if printApply(reps, dry) > 0 {
			return 1
		}
		return 0
	case *snap != "":
		m, warns, err := snapshot(*snap)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := writeOut(string(b)+"\n", *output); err != nil {
			errMsg(err.Error())
			return 2
		}
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning: "+w)
		}
		fmt.Fprintf(os.Stderr, "# snapshot of %s: %d entries\n", m.Root, len(m.Files))
		return 0
	case *verify != "":
		m, err := loadManifest(*verify)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		root := m.Root
		if len(args) > 0 {
			root = args[0]
		}
		rows, same, err := verifyManifest(m, root)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		if *format == "json" {
			if rows == nil {
				rows = []verifyRow{}
			}
			b, _ := json.MarshalIndent(map[string]any{"manifest": *verify, "root": root, "unchanged": same, "changes": rows}, "", "  ")
			writeOut(string(b)+"\n", *output)
		} else {
			banner()
			sectionHeader("Integrity Verify")
			printVerify(m, root, rows, same)
		}
		if len(rows) > 0 {
			return 1
		}
		return 0
	case *mergeBase != "":
		if len(args) != 2 {
			errMsg("--merge BASE needs the two edited versions: A B")
			return 2
		}
		files := make([]textFile, 3)
		for i, p := range []string{*mergeBase, args[0], args[1]} {
			t, err := loadFile(p)
			if err != nil {
				errMsg(err.Error())
				return 2
			}
			if t.binary {
				errMsg(p + ": binary files cannot be merged line by line")
				return 2
			}
			files[i] = t
		}
		r := merge3(files[0], files[1], files[2], args[0], args[1], *diff3)
		if *output != "" || noColor {
			if err := writeOut(string(r.bytes()), *output); err != nil {
				errMsg(err.Error())
				return 2
			}
		} else {
			banner()
			sectionHeader(fmt.Sprintf("3-Way Merge — %d auto-merged chunk(s), %d conflict(s)", r.clean, r.conflicts))
			printMerge(r, 0)
		}
		if r.conflicts > 0 {
			fmt.Fprintf(os.Stderr, "# %d conflict(s) — resolve the <<<<<<< ======= >>>>>>> blocks\n", r.conflicts)
			return 1
		}
		return 0
	case len(args) == 0:
		mainMenu()
		return 0
	case len(args) != 2:
		errMsg("need exactly two paths (A B) — see --help")
		return 2
	}
	if *format != "" && *format != "unified" && *format != "json" && *format != "html" {
		errMsg("unknown --format " + *format + " (unified|json|html)")
		return 2
	}
	pa, pb := args[0], args[1]
	isDir := func(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
	// diff convention: a file against a directory means the same name inside it
	if !isDir(pa) && isDir(pb) {
		pb = filepath.Join(pb, filepath.Base(pa))
	} else if isDir(pa) && !isDir(pb) && pb != "-" {
		pa = filepath.Join(pa, filepath.Base(pb))
	}
	tty := !noColor && *output == ""
	if *semantic {
		cs, err := semanticFiles(pa, pb)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		if *format == "json" {
			b, _ := json.MarshalIndent(map[string]any{"a": pa, "b": pb, "changes": cs}, "", "  ")
			writeOut(string(b)+"\n", *output)
		} else if tty {
			banner()
			sectionHeader("Semantic Diff")
			printSemantic(pa, pb, cs)
		} else {
			var sb strings.Builder
			for _, c := range cs {
				switch c.Kind {
				case "added":
					fmt.Fprintf(&sb, "added    %s: %s\n", c.Path, compact(c.New))
				case "removed":
					fmt.Fprintf(&sb, "removed  %s: %s\n", c.Path, compact(c.Old))
				default:
					fmt.Fprintf(&sb, "%-8s %s: %s -> %s\n", c.Kind, c.Path, compact(c.Old), compact(c.New))
				}
			}
			writeOut(sb.String(), *output)
		}
		if len(cs) > 0 {
			return 1
		}
		return 0
	}

	if *format == "json" || *format == "html" {
		r, err := buildReportFor(pa, pb)
		if err != nil {
			errMsg(err.Error())
			return 2
		}
		out := r.json()
		if *format == "html" {
			out = r.html()
		}
		if err := writeOut(out, *output); err != nil {
			errMsg(err.Error())
			return 2
		}
		for _, f := range r.Files {
			if !f.Identical {
				return 1
			}
		}
		for _, d := range r.Dir {
			if d.Status != "same" {
				return 1
			}
		}
		return 0
	}

	if isDir(pa) && isDir(pb) {
		rs, warns := compareDirs(pa, pb)
		differs := false
		for _, r := range rs {
			if r.Status != "same" {
				differs = true
			}
		}
		switch {
		case brief:
			for _, r := range visibleDiffs(rs) {
				fmt.Printf("%-8s %s\n", r.Status, r.Path)
			}
		case stat:
			total := 0
			for _, r := range rs {
				if r.Status != "changed" || r.Note != "" {
					continue
				}
				a, e1 := loadFile(filepath.Join(pa, r.Path))
				b, e2 := loadFile(filepath.Join(pb, r.Path))
				if e1 != nil || e2 != nil || a.binary || b.binary {
					fmt.Printf(" %s | Bin\n", r.Path)
					continue
				}
				ed := diffLines(a, b)
				st := statsOf(a, b, ed, buildHunks(a, b, ed, opt.ctx))
				total++
				fmt.Println(statLine(r.Path, st.Added, st.Removed, 40, tty))
			}
			c := dirCounts(rs)
			fmt.Printf(" %d file(s) changed, %d only in A, %d only in B\n", total, c["only-a"], c["only-b"])
		case tty && !sbs:
			banner()
			sectionHeader("Directory Compare")
			printDir(pa, pb, rs, warns, 0)
		default:
			if err := writeOut(dirPatch(pa, pb, rs), *output); err != nil {
				errMsg(err.Error())
				return 2
			}
			for _, w := range warns {
				fmt.Fprintln(os.Stderr, "warning: "+w)
			}
		}
		if differs {
			return 1
		}
		return 0
	}

	a, err := loadFile(pa)
	if err != nil {
		errMsg(err.Error())
		return 2
	}
	b, err := loadFile(pb)
	if err != nil {
		errMsg(err.Error())
		return 2
	}
	if a.binary || b.binary {
		same := bytes.Equal(a.data, b.data)
		switch {
		case same && !tty:
		case !tty || brief:
			if !same {
				fmt.Printf("Binary files %s and %s differ\n", pa, pb)
			}
		default:
			banner()
			sectionHeader("Binary Diff")
			printBinary(a, b, 8)
		}
		if same {
			return 0
		}
		return 1
	}
	ed := diffLines(a, b)
	hs := buildHunks(a, b, ed, opt.ctx)
	if len(hs) == 0 {
		return 0
	}
	switch {
	case brief:
		fmt.Printf("Files %s and %s differ\n", pa, pb)
	case stat:
		st := statsOf(a, b, ed, hs)
		fmt.Println(statLine(filepath.Base(pb), st.Added, st.Removed, 40, tty))
		fmt.Printf(" 1 file changed, %d insertion(s)(+), %d deletion(s)(-) · %.1f%% similar\n", st.Added, st.Removed, st.Similarity)
	case sbs:
		printSideBySide(a, b, hs, *width)
	case tty:
		printUnifiedOrdered(a, b, hs, 0)
	default:
		if err := writeOut(unifiedText(a, b, "", "", hs), *output); err != nil {
			errMsg(err.Error())
			return 2
		}
	}
	return 1
}

// ---------------------------------------------------------------------------
// Help & Usage  (interactive menu [H])
// Page 1: in-app guide (what the tool does, every menu option, tips).
// Page 2: the full command-line reference (same text as --help).
// ---------------------------------------------------------------------------

type owlHelpItem struct{ key, text string }

func owlHelpWrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, w := range words {
			if cur != "" && len([]rune(cur))+1+len([]rune(w)) > width {
				out = append(out, cur)
				cur = w
			} else if cur == "" {
				cur = w
			} else {
				cur += " " + w
			}
		}
		out = append(out, cur)
	}
	return out
}

func owlHelpTitle(t string) {
	fmt.Println()
	fmt.Println("  " + col("▸ "+t, B+CY))
}

func owlHelpPara(lines []string) {
	for _, l := range lines {
		for _, w := range owlHelpWrap(l, 60) {
			fmt.Println("    " + col(w, W))
		}
	}
}

func owlHelpRows(items []owlHelpItem) {
	kw := 0
	for _, it := range items {
		if n := len([]rune(it.key)); n > kw {
			kw = n
		}
	}
	for _, it := range items {
		k := "[" + it.key + "]" + strings.Repeat(" ", kw-len([]rune(it.key)))
		for i, w := range owlHelpWrap(it.text, 56-kw) {
			if i == 0 {
				fmt.Println("    " + col(k, YLW+B) + "  " + col(w, W))
			} else {
				fmt.Println("    " + strings.Repeat(" ", kw+2) + "  " + col(w, W))
			}
		}
	}
}

func owlHelpRule() { fmt.Println(col(strings.Repeat("─", 66), CY)) }

func tuiHelp() {
	clear()
	banner()
	owlHelpRule()
	fmt.Println("  " + col("⬡  Help & Usage — "+APPNAME, B+CY))
	owlHelpRule()
	owlHelpTitle("WHAT IT DOES")
	owlHelpPara(owlHelpAbout)
	owlHelpTitle("HOW TO USE THE MENU")
	owlHelpPara(owlHelpFlow)
	owlHelpTitle("MENU OPTIONS")
	owlHelpRows(owlHelpMenu)
	owlHelpTitle("TIPS")
	owlHelpRows(owlHelpTips)
	fmt.Println()
	owlHelpRule()
	fmt.Println("  " + col("Next page: command-line flags & copy-paste examples", DIM))
	pause()
	clear()
	owlHelpCLI()
	fmt.Println()
	fmt.Println("  " + col("Tip: the same reference is printed by  "+APPNAME+" --help", DIM))
	pause()
}

var owlHelpAbout = []string{
	"OFileDiff compares files and folders. It produces GNU-compatible unified diffs and patches with colour, word-level highlighting and moved-block detection. It also applies patches (dry-run, reverse, -p N), compares binary files byte by byte, compares whole directory trees, and compares JSON / YAML as data (key order and formatting are ignored). It can do a 3-way merge with conflict markers like git, take a SHA-256 snapshot of a folder to detect later tampering, and write a self-contained HTML report.",
}

var owlHelpFlow = []string{
	"Type the number of an option and press Enter. Most options ask for A (the original) and B (the modified version). The menu header shows the current diff options; change them with [13]. They apply to every later diff. [H] opens this help, [0] or a blank line quits.",
}

var owlHelpMenu = []owlHelpItem{
	{"1", "Text diff: enter file A and file B. Shows a coloured unified diff with the changed words highlighted."},
	{"2", "Side-by-side view: A and B in two columns with line numbers."},
	{"3", "Write unified patch: enter A and B (files or directories) and the patch file name (blank = out.patch). The result works with GNU patch and git apply."},
	{"4", "Apply a patch: enter the patch file, the folder to apply it in (blank = .), the -p strip level (blank = 0) and whether to reverse (undo) it. It always does a dry run first and asks y/N before changing files."},
	{"5", "Diff statistics: diffstat (+/- per file), similarity percentage and number of hunks."},
	{"6", "Binary diff: enter two binary files. Shows the changed byte ranges and a side-by-side hex view."},
	{"7", "Directory compare: enter directory A and B and optional names to skip (e.g. .git node_modules). Lists changed files, files only in A or only in B, and type changes."},
	{"8", "Semantic diff: enter two .json or .yaml files. Compares them as data and lists changed keys and values."},
	{"9", "3-way merge: enter base (common ancestor), A (yours) and B (theirs). Choose y to show the base in conflicts (diff3 style) and, optionally, a file to save the merged result."},
	{"10", "Snapshot a directory: enter a folder and a manifest file (blank = ofilediff-manifest.json). Stores the SHA-256 of every file."},
	{"11", "Verify a snapshot: enter the manifest (and the folder if it moved). Shows files added, removed or modified since the snapshot."},
	{"12", "HTML report: enter A and B and an output file (blank = ofilediff-report.html) to get a single page you can open in any browser."},
	{"13", "Options: context lines, ignore whitespace (-w / -b), ignore case (-i), ignore blank lines (-B), strip CR (Windows line endings), patience algorithm and moved-block colouring."},
	{"14", "Run demo: an sshd_config hardening diff."},
	{"15", "Self-test: minimality, round-trip and GNU patch compatibility checks."},
}

var owlHelpTips = []owlHelpItem{
	{"*", "Files with Windows line endings look completely changed. Turn on strip CR in [13] (or use --strip-trailing-cr)."},
	{"*", "For code, the patience algorithm in [13] usually gives cleaner, easier-to-read diffs."},
	{"*", "Snapshot /etc or a web root after a clean install, then verify later to spot tampering."},
}

func owlHelpCLI() { usage() }
