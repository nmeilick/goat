package goat

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines shown around each change.
const diffContext = 3

// ANSI colors for the dry-run diff.
const (
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
	ansiReset = "\x1b[0m"
)

/*
diffOp is one line of an edit script: kept (' '), removed ('-') or
added ('+'). noNewline marks the file's final line when that side lacks
its trailing newline.
*/
type diffOp struct {
	kind      byte
	line      string
	noNewline bool
}

/*
UnifiedDiff renders old and new as a unified diff with three lines of
context, using "---"/"+++" headers ("/dev/null" names an absent side),
a "\ No newline at end of file" marker when a side lacks its trailing
newline, and ANSI color for hunk headers and added/removed lines when
color is set. It returns an empty string when the contents are identical.
*/
func UnifiedDiff(oldName, newName string, old, new []byte, color bool) string {
	ops := diffOps(splitLines(old), splitLines(new))

	oldNoNL := len(old) > 0 && old[len(old)-1] != '\n'
	newNoNL := len(new) > 0 && new[len(new)-1] != '\n'
	if oldNoNL || newNoNL {
		ops = markMissingNewline(ops, oldNoNL, newNoNL)
	}

	var changed []int
	for i, o := range ops {
		if o.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)

	/*
		Group changes into hunks: changes separated by at most twice the
		context share one hunk, padded with diffContext unchanged lines.
	*/
	for start := 0; start < len(changed); {
		end := start
		for end+1 < len(changed) && changed[end+1]-changed[end] <= 2*diffContext+1 {
			end++
		}
		lo := max(changed[start]-diffContext, 0)
		hi := min(changed[end]+diffContext+1, len(ops))
		writeHunk(&out, ops, lo, hi, color)
		start = end + 1
	}
	return out.String()
}

/*
splitLines returns the content's lines without terminators; a trailing
newline does not produce a phantom empty line.
*/
func splitLines(content []byte) []string {
	s := strings.TrimSuffix(string(content), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

/*
diffOps computes a line edit script from a to b via a longest common
subsequence. Source files are kilobytes, so the quadratic table is cheap.
*/
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', line: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{kind: '-', line: a[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: '-', line: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: '+', line: b[j]})
	}
	return reorderOps(ops)
}

/*
markMissingNewline records which side's final line lacks its trailing
newline, so writeHunk can emit the "\ No newline at end of file" marker.
When the final lines are equal and differ only in that newline, the
shared op is split into a removal and an addition — otherwise the
difference would be invisible.
*/
func markMissingNewline(ops []diffOp, oldNoNL, newNoNL bool) []diffOp {
	li, lj := -1, -1 // last op consuming a line of old / of new
	for i, o := range ops {
		if o.kind != '+' {
			li = i
		}
		if o.kind != '-' {
			lj = i
		}
	}
	if li >= 0 && li == lj {
		if oldNoNL == newNoNL {
			/*
				Identical final lines: one marker when both sides lack the
				trailing newline (GNU diff does the same), none otherwise.
			*/
			ops[li].noNewline = oldNoNL
			return ops
		}
		line := ops[li].line
		out := make([]diffOp, 0, len(ops)+1)
		out = append(out, ops[:li]...)
		out = append(out, diffOp{kind: '-', line: line, noNewline: oldNoNL}, diffOp{kind: '+', line: line, noNewline: newNoNL})
		return append(out, ops[li+1:]...)
	}
	if li >= 0 {
		ops[li].noNewline = oldNoNL
	}
	if lj >= 0 {
		ops[lj].noNewline = newNoNL
	}
	return ops
}

/*
reorderOps moves removals before additions inside each change block so a
replaced line reads as "-old" then "+new".
*/
func reorderOps(ops []diffOp) []diffOp {
	var out []diffOp
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			out = append(out, ops[i])
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].kind != ' ' {
			j++
		}
		for k := i; k < j; k++ {
			if ops[k].kind == '-' {
				out = append(out, ops[k])
			}
		}
		for k := i; k < j; k++ {
			if ops[k].kind == '+' {
				out = append(out, ops[k])
			}
		}
		i = j
	}
	return out
}

/*
writeHunk emits one hunk spanning ops[lo:hi], deriving the @@ header's
line ranges from the op kinds.
*/
func writeHunk(out *strings.Builder, ops []diffOp, lo, hi int, color bool) {
	oldStart, newStart := 1, 1
	for _, o := range ops[:lo] {
		if o.kind != '+' {
			oldStart++
		}
		if o.kind != '-' {
			newStart++
		}
	}
	oldCount, newCount := 0, 0
	for _, o := range ops[lo:hi] {
		if o.kind != '+' {
			oldCount++
		}
		if o.kind != '-' {
			newCount++
		}
	}
	// GNU diff addresses an empty side by the line the hunk precedes.
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	writeColored(out, fmt.Sprintf("@@ -%s +%s @@", rangeString(oldStart, oldCount), rangeString(newStart, newCount)), ansiCyan, color)
	for _, o := range ops[lo:hi] {
		switch o.kind {
		case '-':
			writeColored(out, "-"+o.line, ansiRed, color)
		case '+':
			writeColored(out, "+"+o.line, ansiGreen, color)
		default:
			fmt.Fprintln(out, " "+o.line)
		}
		if o.noNewline {
			fmt.Fprintln(out, `\ No newline at end of file`)
		}
	}
}

/*
rangeString formats one side of a hunk header, omitting the count for a
single line as GNU diff does.
*/
func rangeString(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func writeColored(out *strings.Builder, line, code string, color bool) {
	if color {
		fmt.Fprintf(out, "%s%s%s\n", code, line, ansiReset)
		return
	}
	fmt.Fprintln(out, line)
}
