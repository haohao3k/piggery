package cli

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sting8k/piggery/manifests"
)

// The tick list `setup --outdated` shows for what the user changed: one line per built-in template or
// shared prompt, nothing ticked (keep yours) until the user ticks it. It draws in the colours of top.
// The model knows nothing of templates: items in, the ticked names out, so a test drives it with keys.

// pickModel is the list and, over it, the diff of one line against the version piggery ships.
type pickModel struct {
	head   []string // the lines above the list
	items  []upgradeItem
	tick   []bool
	cur    int
	diffOf func(upgradeItem) ([]diffLine, error)
	pane   []diffLine // the diff being read; nil while the list shows
	off    int        // the first line of the diff in view
	w, h   int
	done   bool // enter: the ticks stand; otherwise (esc) they are dropped: keep all
}

func newPick(head []string, items []upgradeItem, diffOf func(upgradeItem) ([]diffLine, error)) *pickModel {
	return &pickModel{head: head, items: items, tick: make([]bool, len(items)), diffOf: diffOf, w: 80, h: 24}
}

// Chosen is the names ticked, once the user pressed enter; none after esc.
func (m *pickModel) Chosen() (names []string) {
	for i, it := range m.items {
		if m.done && m.tick[i] {
			names = append(names, it.Name)
		}
	}
	return names
}

func (m *pickModel) Init() tea.Cmd { return nil }

func (m *pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if m.pane != nil {
			m.readKey(msg.String())
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			m.cur = max(m.cur-1, 0)
		case "down", "j":
			m.cur = min(m.cur+1, len(m.items)-1)
		case "space", " ", "x":
			m.tick[m.cur] = !m.tick[m.cur]
		case "d":
			lines, err := m.diffOf(m.items[m.cur])
			if err != nil {
				lines = []diffLine{{'!', "could not read the files: " + err.Error()}}
			}
			if len(lines) == 0 {
				lines = []diffLine{{'@', "no difference"}}
			}
			m.pane, m.off = lines, 0
		case "enter":
			m.done = true
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// readKey moves in the diff; esc (or d, q, enter) goes back to the list.
func (m *pickModel) readKey(k string) {
	page := max(m.h-4, 1)
	switch k {
	case "up", "k":
		m.off--
	case "down", "j":
		m.off++
	case "pgup", "b":
		m.off -= page
	case "pgdown", "f", "space", " ":
		m.off += page
	case "home", "g":
		m.off = 0
	case "end", "G":
		m.off = len(m.pane)
	case "esc", "d", "q", "enter":
		m.pane = nil
		return
	}
	m.off = max(min(m.off, len(m.pane)-page), 0)
}

func (m *pickModel) View() tea.View {
	if m.done {
		return tea.NewView("") // the answer is printed by the caller, in the scrollback
	}
	if m.pane != nil {
		v := tea.NewView(m.readView())
		v.AltScreen = true // a diff wants the screen; the list stays inline
		return v
	}
	var b strings.Builder
	for _, l := range m.head {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
	name := 0
	for _, it := range m.items {
		name = max(name, len(it.Name))
	}
	for i, it := range m.items {
		box, label := "[ ]", lipgloss.NewStyle()
		if m.tick[i] {
			box = lipgloss.NewStyle().Foreground(colSuccess).Render("[x]")
		}
		mark := "  "
		if i == m.cur {
			mark, label = lipgloss.NewStyle().Foreground(colPrimary).Render("› "), label.Bold(true).Foreground(colPrimary)
		}
		row := fmt.Sprintf("%s%s %s  %s", mark, box, label.Render(fmt.Sprintf("%-*s", name, it.Name)), stMuted.Render(it.Note))
		b.WriteString(ansi.Truncate(row, max(m.w-1, 20), "…") + "\n")
	}
	b.WriteString("\n" + stRule.Render("space tick · d diff · enter done · esc keep all"))
	return tea.NewView(b.String())
}

// readView is the diff pane: the lines from the offset that fit, and a footer with the legend.
func (m *pickModel) readView() string {
	room := max(m.h-3, 1)
	var b strings.Builder
	for _, l := range m.pane[min(m.off, len(m.pane)):min(m.off+room, len(m.pane))] {
		b.WriteString(ansi.Truncate(l.styled(), max(m.w-1, 20), "…") + "\n")
	}
	return b.String() + "\n" + stRule.Render("- yours  + new version · ↑/↓ pgup/pgdn scroll · esc back")
}

// askTicks runs the list on the terminal and returns the names ticked (none after esc).
func askTicks(in, out *os.File, head []string, items []upgradeItem, diffOf func(upgradeItem) ([]diffLine, error)) ([]string, error) {
	usePalette(lipgloss.HasDarkBackground(in, out)) // as top decides
	final, err := tea.NewProgram(newPick(head, items, diffOf), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return nil, err
	}
	return final.(*pickModel).Chosen(), nil
}

// diffLine is one line of a diff: op is ' ' (same), '-' (yours), '+' (the new version), '@' (a header
// or a gap) or '!' (a note).
type diffLine struct {
	op   byte
	text string
}

func (l diffLine) styled() string {
	switch l.op {
	case '+':
		return lipgloss.NewStyle().Foreground(colSuccess).Render("+ " + l.text)
	case '-':
		return lipgloss.NewStyle().Foreground(colError).Render("- " + l.text)
	case '@':
		return stTitle.Render(l.text)
	case '!':
		return lipgloss.NewStyle().Foreground(colWarning).Render(l.text)
	}
	return stMuted.Render("  " + l.text)
}

// diffPairs is the diff of each file that differs, yours against the new version, as the pane shows it.
func diffPairs(pairs []manifests.Pair) []diffLine {
	var out []diffLine
	for _, p := range pairs {
		out = append(out, diffLine{'@', p.Rel})
		switch {
		case p.Yours == nil:
			out = append(out, diffLine{'!', "you removed this file"})
			out = append(out, lineDiff("", string(p.Builtin), 2)...)
		case p.Builtin == nil:
			out = append(out, diffLine{'!', "your file; the new version has none (it would go to the backup)"})
		default:
			out = append(out, lineDiff(string(p.Yours), string(p.Builtin), 2)...)
		}
	}
	return out
}

// lineDiff is the lines of a against b with ctx lines of context around each change and a gap line
// between far apart ones. A pair of texts too long for the table is shown as all of a gone and all of b new.
func lineDiff(a, b string, ctx int) []diffLine {
	x, y := splitLines(a), splitLines(b)
	var ops []diffLine
	if len(x)*len(y) > 4_000_000 {
		for _, l := range x {
			ops = append(ops, diffLine{'-', l})
		}
		for _, l := range y {
			ops = append(ops, diffLine{'+', l})
		}
		return ops
	}
	// lcs[i][j]: the longest common run of x[i:] and y[j:]
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < len(x) || j < len(y); {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, diffLine{' ', x[i]})
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, diffLine{'+', y[j]})
			j++
		default:
			ops = append(ops, diffLine{'-', x[i]})
			i++
		}
	}
	keep := make([]bool, len(ops)) // a line near a change
	for i, o := range ops {
		if o.op == ' ' {
			continue
		}
		for k := max(i-ctx, 0); k <= min(i+ctx, len(ops)-1); k++ {
			keep[k] = true
		}
	}
	var out []diffLine
	for i, o := range ops {
		switch {
		case keep[i]:
			out = append(out, o)
		case len(out) > 0 && out[len(out)-1].op != '@':
			out = append(out, diffLine{'@', "…"})
		}
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
