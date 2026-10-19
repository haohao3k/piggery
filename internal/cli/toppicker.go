package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/sting8k/piggery/internal/core"
	"github.com/sting8k/piggery/internal/view"
)

// The model picker: a box over top, opened by a click on the model row of a live headless worker's
// Tail. A text input fuzzy-filters the models the daemon lists for that worker (verb proto.VerbModels),
// a row of thinking levels is chosen beside it, Enter applies through the verb `model`, Esc closes.

// pickLevels are the thinking levels the picker offers, in the order it shows them.
var pickLevels = []string{"low", "medium", "high", "xhigh", "max"}

const (
	pickRows    = 8                      // models listed at once
	doubleClick = 400 * time.Millisecond // terminals send no double click: two clicks this close on one row are one
)

// modelPicker is the open picker's state.
type modelPicker struct {
	id, name string   // the worker
	cur      string   // its model now, as ps says it
	curLevel int      // its thinking level's place in pickLevels, -1 when it is none of them
	models   []string // what the daemon lists, once loaded
	loading  bool     // the list is being asked for
	noList   string   // why there is no list: the typed text is applied instead
	input    string
	sel      int // place in the filtered list; -1: none (the model stays)
	top      int // the first listed model shown; moves only when the selection would leave the window
	lastRow  int // the model row last clicked, and when: a second click on it within doubleClick applies it
	lastAt   time.Time
	level    int // the chosen level's place in pickLevels; -1: none (the level stays)
	busy     bool
	note     string // the daemon's refusal, shown in the box
}

type modelsLoaded struct {
	id     string
	models []string
	err    error
}

type modelApplied struct {
	id  string
	err error
}

// open starts a picker for mem and asks for its model list on a connection of its own.
func (m *topModel) openPicker(mem core.MemberState) tea.Cmd {
	p := &modelPicker{id: mem.ID, name: mem.Name, cur: mem.Model, loading: true, level: -1, curLevel: -1, sel: -1, lastRow: -1}
	for i, l := range pickLevels {
		if l == mem.Thinking {
			p.level, p.curLevel = i, i
		}
	}
	m.pick, m.hover = p, ""
	list := m.listModels
	return func() tea.Msg {
		if list == nil {
			return modelsLoaded{id: p.id, err: fmt.Errorf("no connection")}
		}
		models, err := list(p.id)
		return modelsLoaded{id: p.id, models: models, err: err}
	}
}

// filtered is the models the input matches, best first; all of them while it is empty.
func (p *modelPicker) filtered() []string {
	if strings.TrimSpace(p.input) == "" {
		return p.models
	}
	var out []string
	for _, r := range fuzzy.Find(strings.TrimSpace(p.input), p.models) {
		out = append(out, r.Str)
	}
	return out
}

// loaded sets the list; the current model starts selected when it is in it.
func (p *modelPicker) loaded(msg modelsLoaded) {
	p.loading = false
	if msg.err != nil {
		p.noList = "no model list: " + msg.err.Error()
		return
	}
	p.models = msg.models
	if len(p.models) == 0 {
		p.noList = "no model list"
		return
	}
	for i, name := range p.models {
		if name == p.cur {
			p.sel = i
		}
	}
}

// args is what Enter applies: the model chosen (the typed text when nothing in the list matches or
// there is no list) and the level chosen, each only when it differs from what the worker has.
func (p *modelPicker) args() core.ModelArgs {
	a := core.ModelArgs{AdminTarget: core.AdminTarget{Target: p.id}}
	chosen := strings.TrimSpace(p.input)
	if list := p.filtered(); p.noList == "" && len(list) > 0 {
		chosen = ""
		if p.sel >= 0 && p.sel < len(list) {
			chosen = list[p.sel]
		}
	}
	if chosen != "" && chosen != p.cur {
		a.Model = chosen
	}
	if p.level >= 0 && p.level != p.curLevel {
		a.Thinking = pickLevels[p.level]
	}
	return a
}

// typed reports whether s is text a person typed: printable runes only. Text that holds ESC or another
// control rune (a mouse report that arrived as a key) is not typing and must not reach the filter.
func typed(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// pickKey handles a key while the picker is open.
func (m *topModel) pickKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.pick
	if p.busy {
		return nil
	}
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		m.pick = nil
	case "enter":
		return m.pickApply()
	case "up":
		p.move(-1)
	case "down":
		p.move(1)
	case "left":
		p.level = (max(p.level, 0) + len(pickLevels) - 1) % len(pickLevels) // from none: the last
	case "right":
		p.level = (p.level + 1) % len(pickLevels) // from none (-1): the first
	case "backspace":
		if r := []rune(p.input); len(r) > 0 {
			p.input = string(r[:len(r)-1])
			p.sel, p.top = 0, 0
		}
	default:
		if typed(msg.Text) {
			p.input += msg.Text
			p.sel, p.top = 0, 0
		}
	}
	return nil
}

// move selects the model d places further in the filtered list, staying in it.
func (p *modelPicker) move(d int) {
	p.sel = min(max(p.sel+d, 0), max(len(p.filtered())-1, 0))
}

// pickApply sends what the picker chose through the verb `model`; closes it when nothing changed.
func (m *topModel) pickApply() tea.Cmd {
	p := m.pick
	a := p.args()
	if a.Model == "" && a.Thinking == "" {
		m.pick = nil
		return nil
	}
	p.busy, p.note = true, ""
	set := m.setModel
	return func() tea.Msg {
		if set == nil {
			return modelApplied{id: a.Target, err: fmt.Errorf("no connection")}
		}
		return modelApplied{id: a.Target, err: set(a)}
	}
}

// pickClick handles a click while the picker is open: a model row selects it (a second click on it
// within doubleClick applies it, like Enter), a level chooses it.
func (m *topModel) pickClick(x, y int) tea.Cmd {
	h, ok := m.at(x, y)
	if !ok || m.pick.busy {
		return nil
	}
	kind, n, _ := strings.Cut(h.opt, ":")
	i, err := strconv.Atoi(n)
	switch {
	case err != nil:
	case kind == "m":
		p := m.pick
		again := p.lastRow == i && time.Since(p.lastAt) <= doubleClick
		p.sel, p.lastRow, p.lastAt = i, i, time.Now()
		if again {
			return m.pickApply()
		}
	case kind == "l":
		m.pick.level = i
	}
	return nil
}

// pickerBox is the picker as lines in a bw-wide box, and the hits of its rows and levels relative to
// the box's top-left corner (y, x0 and x1 are the box's own).
func (m *topModel) pickerBox(bw int) ([]string, []hit) {
	p := m.pick
	inner := bw - 2
	var lines []string
	var hits []hit
	add := func(s string) { lines = append(lines, " "+s) }

	now := view.ModelLabel(p.cur, "")
	if p.curLevel >= 0 {
		now = view.ModelLabel(p.cur, pickLevels[p.curLevel])
	}
	add(stMuted.Render("now  ") + now)
	add(stMuted.Render("> ") + p.input + stMuted.Render("▏"))

	list := p.filtered()
	switch { // the window follows the selection only when it would leave it
	case p.sel >= 0 && p.sel < p.top:
		p.top = p.sel
	case p.sel >= p.top+pickRows:
		p.top = p.sel - pickRows + 1
	}
	p.top = min(max(p.top, 0), max(len(list)-pickRows, 0))
	start := p.top
	for i := 0; i < pickRows; i++ {
		switch {
		case i == 0 && p.loading:
			add(stMuted.Render("Loading models…"))
		case i == 0 && (p.noList != "" || len(list) == 0):
			why := p.noList
			if why == "" {
				why = "no match"
			}
			add(stMuted.Render(truncate(why+" · enter applies what you typed", inner-2)))
		case !p.loading && p.noList == "" && start+i < len(list):
			name := list[start+i]
			mark := " "
			if name == p.cur {
				mark = "●"
			}
			text := truncate(mark+" "+name, inner-3)
			text += strings.Repeat(" ", max(inner-3-lipgloss.Width(text), 0))
			if start+i == p.sel {
				lines = append(lines, lipgloss.NewStyle().Foreground(colPrimary).Render("▌ ")+lipgloss.NewStyle().Background(colSurface).Render(text)+" ")
			} else {
				lines = append(lines, "  "+text+" ")
			}
			hits = append(hits, hit{y: len(lines), x0: 1, x1: bw - 1, opt: fmt.Sprintf("m:%d", start+i)})
		default:
			add("")
		}
	}

	add("")
	row := stMuted.Render("thinking ")
	x := 1 + 1 + lipgloss.Width("thinking ")
	for i, l := range pickLevels {
		cell := " " + l + " "
		w := lipgloss.Width(cell)
		switch {
		case i == p.level:
			cell = pill(l, colPrimary, colOnMain)
		case i == p.curLevel:
			cell = stTitle.Render(cell)
		default:
			cell = stMuted.Render(cell)
		}
		row += cell
		hits = append(hits, hit{y: len(lines) + 1, x0: x, x1: x + w, opt: fmt.Sprintf("l:%d", i)})
		x += w
	}
	add(row)
	add(stMuted.Render("pi and Codex apply it from the next turn."))
	switch {
	case p.busy:
		add(stMuted.Render("Applying…"))
	case p.note != "":
		add(lipgloss.NewStyle().Foreground(colError).Render(truncate(p.note, inner-2)))
	default:
		add("")
	}
	add(stMuted.Render("↑/↓ model   ←/→ level   enter apply   esc close"))
	var above, below string
	if p.noList == "" && !p.loading {
		if start > 0 {
			above = fmt.Sprintf("↑ %d", start)
		}
		if n := len(list) - start - pickRows; n > 0 {
			below = fmt.Sprintf("↓ %d", n)
		}
	}
	return boxMarked(stTitle.Render("Model · "+p.name), lines, bw, len(lines)+2, above, below), hits
}

// overlay draws the picker, centred, over the frame's lines, and replaces the frame's hits with its
// own: nothing under the box answers clicks.
func (m *topModel) overlay(frame []string, width int) []string {
	bw := min(max(width-8, 30), 64)
	boxLines, hits := m.pickerBox(bw)
	x0, y0 := (width-bw)/2, max((len(frame)-len(boxLines))/2, 1)
	out := append([]string(nil), frame...)
	for i, bl := range boxLines {
		if y0+i >= len(out) {
			break
		}
		line := out[y0+i]
		left := ansi.Truncate(line, x0, "")
		left += strings.Repeat(" ", max(x0-lipgloss.Width(left), 0))
		out[y0+i] = left + bl + ansi.TruncateLeft(line, x0+bw, "")
	}
	m.hits = m.hits[:0]
	for _, h := range hits {
		h.y, h.x0, h.x1, h.list = y0+h.y, x0+h.x0, x0+h.x1, false
		m.hits = append(m.hits, h)
	}
	return out
}
