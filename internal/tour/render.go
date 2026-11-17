package tour

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

// cell is one character of the diagram with its role.
type cell struct {
	r    rune
	role int
}

const (
	roleBlank = iota
	roleLane
	roleHead
	roleActive
	roleDone
	roleTodo
	roleNum
	roleNumActive
)

// Diagram draws the sequence diagram of a tour in width columns, one row
// per arrow. The arrows of step active are highlighted, those of steps
// before it are normal, the rest muted.
func Diagram(t *Tour, active, width int) string {
	n := len(t.Lanes)
	names := make([]string, n)
	for i, l := range t.Lanes {
		names[i] = l.String()
	}
	const margin = 4 // step numbers
	width = max(width, margin+8*n)
	// The lanes share the width by what their arrows need, so the long
	// labels between server and Paygate get the room.
	need := make([]int, n-1)
	for i := range need {
		need[i] = 12
	}
	for _, st := range t.Steps {
		for _, a := range st.Arrows {
			lo, hi := min(a.From, a.To), max(a.From, a.To)
			if hi == lo+1 {
				need[lo] = max(need[lo], len([]rune(a.Label.String()))+6)
			}
		}
	}
	sum := 0
	for _, v := range need {
		sum += v
	}
	pos := make([]int, n)
	pos[0] = margin + (len([]rune(names[0]))+1)/2
	pos[n-1] = width - 1 - (len([]rune(names[n-1]))+1)/2
	acc := 0
	for i := 1; i < n-1; i++ {
		acc += need[i-1]
		pos[i] = pos[0] + (pos[n-1]-pos[0])*acc/sum
	}

	blank := func() []cell {
		row := make([]cell, width)
		for i := range row {
			row[i] = cell{' ', roleBlank}
		}
		for _, p := range pos {
			row[p] = cell{'│', roleLane}
		}
		return row
	}
	put := func(row []cell, at int, s string, role int) {
		for i, r := range []rune(s) {
			if at+i >= 0 && at+i < len(row) {
				row[at+i] = cell{r, role}
			}
		}
	}

	head := make([]cell, width)
	for i := range head {
		head[i] = cell{' ', roleBlank}
	}
	for i, name := range names {
		put(head, pos[i]-len([]rune(name))/2, name, roleHead)
	}
	rows := [][]cell{head}
	for si, st := range t.Steps {
		role := roleTodo
		switch {
		case si == active:
			role = roleActive
		case si < active:
			role = roleDone
		}
		for ai, a := range st.Arrows {
			row := blank()
			if ai == 0 {
				num := strconv.Itoa(si + 1)
				if si == active {
					put(row, 0, "▶", roleNumActive)
					put(row, 2, num, roleNumActive)
				} else {
					put(row, 2, num, roleNum)
				}
			}
			label := " " + a.Label.String() + " "
			if a.From == a.To {
				put(row, pos[a.From]+2, "◆"+label, role)
				rows = append(rows, row)
				continue
			}
			lo, hi := min(pos[a.From], pos[a.To]), max(pos[a.From], pos[a.To])
			seg := []rune(strings.Repeat("─", hi-lo-1))
			if a.From < a.To {
				seg[len(seg)-1] = '▶'
			} else {
				seg[0] = '◀'
			}
			lr := []rune(label)
			if len(lr) > len(seg)-2 {
				lr = append(lr[:max(len(seg)-4, 0)], '…', ' ')
			}
			start := max((len(seg)-len(lr))/2, 1)
			for i, r := range lr {
				if start+i < len(seg)-1 {
					seg[start+i] = r
				}
			}
			put(row, lo+1, string(seg), role)
			rows = append(rows, row)
		}
	}

	styles := map[int]lipgloss.Style{
		roleLane:      lipgloss.NewStyle().Foreground(ui.ColFrame),
		roleHead:      ui.Bold,
		roleActive:    lipgloss.NewStyle().Foreground(ui.ColAccent2).Bold(true),
		roleDone:      ui.Label,
		roleTodo:      ui.Muted,
		roleNum:       ui.Muted,
		roleNumActive: lipgloss.NewStyle().Foreground(ui.ColAccent2).Bold(true),
	}
	var out []string
	for _, row := range rows {
		var b strings.Builder
		for i := 0; i < len(row); {
			j := i
			for j < len(row) && row[j].role == row[i].role {
				j++
			}
			var s strings.Builder
			for _, c := range row[i:j] {
				s.WriteRune(c.r)
			}
			if st, ok := styles[row[i].role]; ok {
				b.WriteString(st.Render(s.String()))
			} else {
				b.WriteString(s.String())
			}
			i = j
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(out, "\n")
}

// jsonLine is one line of pretty-printed JSON.
type jsonLine struct {
	plain, styled string
	path          string
}

// Annotate pretty-prints a JSON body in colour and writes the note of a
// field behind its line, aligned in one column. Bodies that are not JSON
// come back as they are.
func Annotate(body []byte, notes map[string]T) []string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var lines []jsonLine
	if err := writeValue(dec, &lines, "", 0, "", ""); err != nil {
		return strings.Split(string(body), "\n")
	}
	// One column for the notes; a very long line (a URL, a version) gets
	// its note right behind it instead of pushing the column away.
	const longLine = 56
	col := 0
	for _, l := range lines {
		if _, ok := notes[l.path]; ok && l.path != "" && lipgloss.Width(l.plain) <= longLine {
			col = max(col, lipgloss.Width(l.plain))
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.styled
		if note, ok := notes[l.path]; ok && l.path != "" {
			out[i] += strings.Repeat(" ", max(col-lipgloss.Width(l.plain), 0)+3) + noteStyle(note.String())
		}
	}
	return out
}

// noteStyle sets "required" or "Pflicht" apart from the rest of a note.
func noteStyle(s string) string {
	head, rest, found := strings.Cut(s, " · ")
	if !found {
		return ui.Muted.Render(s)
	}
	return ui.Label.Render(head) + ui.Muted.Render(" · "+rest)
}

func writeValue(dec *json.Decoder, lines *[]jsonLine, path string, depth int, keyPlain, keyStyled string) error {
	indent := strings.Repeat("  ", depth)
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	add := func(plain, styled string) {
		*lines = append(*lines, jsonLine{plain: indent + keyPlain + plain, styled: indent + keyStyled + styled, path: path})
	}
	switch v := tok.(type) {
	case json.Delim:
		open, closing := string(v), "}"
		if v == '[' {
			closing = "]"
		}
		add(open, ui.Muted.Render(open))
		for dec.More() {
			var kp, ks, child string
			if v == '{' {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				k, _ := kt.(string)
				q := strconv.Quote(k)
				kp, ks = q+": ", ui.Link.Render(q)+ui.Muted.Render(": ")
				child = k
				if path != "" {
					child = path + "." + k
				}
			} else {
				child = path + "[]"
			}
			if err := writeValue(dec, lines, child, depth+1, kp, ks); err != nil {
				return err
			}
			if dec.More() {
				last := &(*lines)[len(*lines)-1]
				last.plain += ","
				last.styled += ui.Muted.Render(",")
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		*lines = append(*lines, jsonLine{plain: indent + closing, styled: indent + ui.Muted.Render(closing)})
	case string:
		q := strconv.Quote(v)
		add(q, ui.Success.Render(q))
	case json.Number:
		add(v.String(), ui.Warning.Render(v.String()))
	case bool:
		s := strconv.FormatBool(v)
		add(s, ui.Accent.Render(s))
	case nil:
		add("null", ui.Accent.Render("null"))
	default:
		s := fmt.Sprint(v)
		add(s, s)
	}
	return nil
}
