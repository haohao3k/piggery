package cli

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// The Tail in top's sidebar: a participant's latest log lines (tailLine's forms, transcript.go)
// drawn as turns, a blank line between two. A turn opens with what came in (a prompt `›`, mail
// `from lark`); what the participant did sits two cells in under it: its tool calls, problems and
// words. Mail, in or out (`to lark`), is at the left edge wherever it comes, so the exchange reads
// down one column; its direction is a word, not an arrow a reader can take either way. A run of
// tool calls is one line, the latest run, still the last thing in the log, a tree of its last
// calls; a result that went well is not drawn, a failed one is. The latest turn is whole, the
// model's words two lines each; an older turn is what came in, one line of all its calls, and what
// it gave back: the mail it sent, else its last words on one line.

// tailKind is what a Tail item is.
type tailKind int

const (
	itemText    tailKind = iota // the model's words
	itemUser                    // a prompt that is not mail
	itemWarn                    // a problem
	itemRule                    // the end of a turn, or another separator (text: its label)
	itemMailIn                  // mail the participant was given
	itemMailOut                 // mail it sent
	itemTools                   // a run of tool calls
)

// tailCall is one tool call of a run, with how its result went.
type tailCall struct {
	name, args string
	done       bool
	err        string // the failed result's text; "" when it did not fail
}

type tailItem struct {
	kind  tailKind
	text  string // the words; mail: its body
	who   string // mail: the sender (in) or the recipient (out)
	ref   string // mail in: its #N
	more  int    // mail in: the other messages given with it
	mid   bool   // mail in: given after a tool call, inside a turn
	calls []tailCall
}

// toolOpen is the tree of the latest run: how many of its last calls it lists.
const toolOpen = 3

var (
	mailHead = regexp.MustCompile(`\[piggery\] (\d+) new messages?`)
	mailOne  = regexp.MustCompile(`<message id="(#\d+)"[^>]*? sender="([^"]*)"[^>]*>\s*(.*?)\s*(?:</message>|$)`)
	// bold is a **pair** around words: a glob's ** is not one
	bold = regexp.MustCompile(`\*\*(\S(?:[^*\n]*\S)?)\*\*`)
)

// mailIn is the first message of a [piggery] mail block (core.RenderMail) in s.
func mailIn(s string) (tailItem, bool) {
	m := mailOne.FindStringSubmatch(s)
	if m == nil {
		return tailItem{}, false
	}
	it := tailItem{kind: itemMailIn, ref: m[1], who: m[2], text: plain(m[3])}
	if h := mailHead.FindStringSubmatch(s); h != nil {
		n, _ := strconv.Atoi(h[1])
		it.more = max(n-1, 0)
	}
	return it, true
}

// plain is words without their **bold** marks.
func plain(s string) string { return bold.ReplaceAllString(s, "$1") }

// isSendTool reports whether name is piggery's send tool, under any harness's prefix.
func isSendTool(name string) bool {
	for _, p := range []string{"mcp__piggery__", "piggery_"} {
		if strings.TrimPrefix(name, p) == "send" && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// tailItems reads the tail lines into the items the Tail draws.
func tailItems(lines []string) []tailItem {
	var items []tailItem
	run := func() *tailItem { // the run the next call joins: the last item when it is one
		if n := len(items); n > 0 && items[n-1].kind == itemTools {
			return &items[n-1]
		}
		items = append(items, tailItem{kind: itemTools})
		return &items[len(items)-1]
	}
	add := func(it tailItem) {
		if it.kind == itemRule && len(items) == 0 {
			return // no rule first
		}
		if n := len(items); it.kind == itemRule && items[n-1].kind == itemRule {
			if it.text != "" { // none twice; a compaction says more than a turn's end
				items[n-1] = it
			}
			return
		}
		items = append(items, it)
	}
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "> "):
			name, args, _ := strings.Cut(strings.TrimPrefix(l, "> "), " ")
			if isSendTool(name) {
				to, body, _ := strings.Cut(strings.TrimSpace(args), " ")
				add(tailItem{kind: itemMailOut, who: to, text: plain(strings.TrimSpace(body))})
				continue
			}
			if i := strings.LastIndex(args, " (+"); i >= 0 && strings.HasSuffix(args, " more)") {
				args = args[:i] // a transcript's other calls of the message: their results count them
			}
			r := run()
			r.calls = append(r.calls, tailCall{name: name, args: strings.TrimSpace(args)})
		case strings.HasPrefix(l, "< "):
			head, text, _ := strings.Cut(strings.TrimPrefix(l, "< "), ": ")
			name, status := "", head
			if i := strings.LastIndexByte(head, ' '); i >= 0 {
				name, status = head[:i], head[i+1:]
			}
			if !isSendTool(name) && !result(items, name, status == "error", text) {
				// a transcript leaves out the calls of a message that also has words: its result is the call
				c := tailCall{name: name, done: true}
				if status == "error" {
					c.err = cmp.Or(text, "failed")
				}
				r := run()
				r.calls = append(r.calls, c)
			}
			if it, ok := mailIn(text); ok { // mail given after a tool call
				it.mid = true
				add(it)
			}
		case strings.HasPrefix(l, "! "):
			add(tailItem{kind: itemWarn, text: strings.TrimPrefix(l, "! ")})
		case strings.HasPrefix(l, "-- "):
			label := strings.TrimPrefix(l, "-- ")
			if label == "agent end" {
				label = ""
			}
			add(tailItem{kind: itemRule, text: label})
		case strings.HasPrefix(l, "user: "):
			add(tailItem{kind: itemRule}) // a prompt starts a turn: a transcript has no end-of-turn line
			if it, ok := mailIn(l); ok {
				add(it)
			} else {
				add(tailItem{kind: itemUser, text: strings.TrimPrefix(l, "user: ")})
			}
		default:
			add(tailItem{kind: itemText, text: plain(strings.TrimPrefix(l, "assistant: "))})
		}
	}
	if n := len(items); n > 0 && items[n-1].kind == itemRule { // the row says the turn is over
		items = items[:n-1]
	}
	return items
}

// result marks the call a result belongs to: in the last run, the first call of that name still
// waiting, else the first one waiting; false when no call waits for it.
func result(items []tailItem, name string, failed bool, text string) bool {
	if len(items) == 0 || items[len(items)-1].kind != itemTools {
		return false
	}
	r := &items[len(items)-1]
	at := -1
	for i, c := range r.calls {
		if !c.done && (c.name == name || name == "") {
			at = i
			break
		}
	}
	if at < 0 {
		for i, c := range r.calls {
			if !c.done {
				at = i
				break
			}
		}
	}
	if at < 0 {
		return false
	}
	r.calls[at].done = true
	if failed {
		r.calls[at].err = cmp.Or(text, "failed")
	}
	return true
}

// shortArgs is a call's arguments without the worker's own directory: a leading `cd <cwd> &&` goes,
// paths under it are relative, and the home directory is ~.
func shortArgs(args, cwd, home string) string {
	if cwd != "" {
		for _, d := range []string{cwd, `"` + cwd + `"`, "'" + cwd + "'"} {
			for _, sep := range []string{" && ", "; ", " &&\n"} {
				if rest, ok := strings.CutPrefix(args, "cd "+d+sep); ok {
					args = rest
				}
			}
		}
		args = strings.ReplaceAll(args, cwd+"/", "")
	}
	if home != "" {
		args = strings.ReplaceAll(args, home+"/", "~/")
	}
	return args
}

// seg is a styled piece of a Tail line.
type seg struct {
	text  string
	style lipgloss.Style
}

// segs draws pieces in n cells: what does not fit ends in … and the rest is left out.
func segs(n int, parts ...seg) string {
	var b strings.Builder
	for _, p := range parts {
		if n <= 0 {
			break
		}
		t := truncate(p.text, n)
		n -= lipgloss.Width(t)
		b.WriteString(p.style.Render(t))
	}
	return b.String()
}

// turns splits items at their rules: each turn's items, and the rule that ends it (none for the last).
func turns(items []tailItem) (out [][]tailItem, ends []tailItem) {
	start := 0
	for i, it := range items {
		if it.kind == itemRule {
			out, ends = append(out, items[start:i]), append(ends, it)
			start = i + 1
		}
	}
	return append(out, items[start:]), ends
}

// older is an older turn as the Tail keeps it: what came in, the mail given inside it, one run of
// all its calls, its problems, and what it gave back: the mail it sent, else its last words.
func older(turn []tailItem) []tailItem {
	var in, mid, warns, sent []tailItem
	var run *tailItem
	var words *tailItem
	for _, it := range turn {
		switch it.kind {
		case itemUser:
			in = append(in, it)
		case itemMailIn:
			if it.mid {
				mid = append(mid, it)
			} else {
				in = append(in, it)
			}
		case itemTools:
			if run == nil {
				run = &tailItem{kind: itemTools}
			}
			run.calls = append(run.calls, it.calls...)
		case itemWarn:
			warns = append(warns, it)
		case itemMailOut:
			sent = append(sent, it)
		case itemText:
			last := it
			words = &last
		}
	}
	out := append(in, mid...)
	if run != nil {
		out = append(out, *run)
	}
	out = append(out, warns...)
	if len(sent) == 0 && words != nil {
		sent = []tailItem{*words}
	}
	return append(out, sent...)
}

// drawTail is the Tail of items in n cells per line: every line, oldest first.
func drawTail(items []tailItem, n int, cwd, home string) []string {
	tool := lipgloss.NewStyle().Foreground(colTool)
	bad := lipgloss.NewStyle().Foreground(colError)
	link := lipgloss.NewStyle().Foreground(colLink)
	// call is one call's line after its marker: the name and the arguments, or the failure.
	call := func(c tailCall, room int) string {
		if c.err != "" {
			return segs(room, seg{c.name + "  ", bad}, seg{c.err, bad})
		}
		args := shortArgs(c.args, cwd, home)
		if args == "" { // a call known by its result alone
			return segs(room, seg{c.name, tool})
		}
		return segs(room, seg{c.name, tool}, seg{"  " + args, stMuted})
	}
	marker := func(c tailCall, open bool) string {
		switch {
		case c.err != "":
			return bad.Render("✗")
		case !c.done && open:
			return stMuted.Render("◦")
		}
		return lipgloss.NewStyle().Foreground(colSuccess).Render("•")
	}
	const in = "  " // what the participant did, under what came in
	w := n - len(in)
	var out []string
	parts, ends := turns(items)
	for ti, turn := range parts {
		last := ti == len(parts)-1
		if !last {
			turn = older(turn)
		}
		for i, it := range turn {
			switch it.kind {
			case itemUser:
				lines := []string{truncate(it.text, n-2)}
				if last {
					lines = wrapTwo(it.text, n-2)
				}
				for k, l := range lines {
					lead := "› "
					if k > 0 {
						lead = "  "
					}
					out = append(out, stMuted.Render(lead)+l)
				}
			case itemMailIn:
				more := ""
				if it.more > 0 {
					more = fmt.Sprintf("  +%d", it.more)
				}
				body := truncate(it.text, n-lipgloss.Width("from "+it.who+"  "+it.ref+" "+more))
				out = append(out, segs(n, seg{"from ", link}, seg{it.who, stPlain}, seg{"  " + it.ref + " ", stMuted}, seg{body, stPlain}, seg{more, stMuted}))
			case itemText:
				lines := []string{truncate(it.text, w)}
				if last {
					lines = wrapTwo(it.text, w)
				}
				for _, l := range lines {
					out = append(out, in+l)
				}
			case itemWarn:
				out = append(out, in+lipgloss.NewStyle().Foreground(colWarning).Render(truncate(it.text, w)))
			case itemMailOut:
				out = append(out, segs(n, seg{"to ", link}, seg{it.who, stPlain}, seg{"  " + it.text, stPlain}))
			case itemTools:
				open := last && i == len(turn)-1 // the latest run, maybe still going
				if len(it.calls) == 1 {
					c := it.calls[0]
					out = append(out, in+marker(c, open)+" "+call(c, w-2))
					continue
				}
				out = append(out, in+toolSummary(it.calls, open, w))
				if open {
					shown := it.calls[max(len(it.calls)-toolOpen, 0):]
					if hidden := len(it.calls) - len(shown); hidden > 0 {
						out = append(out, in+stMuted.Render(fmt.Sprintf("  +%d", hidden)))
					}
					for j, c := range shown {
						branch := "├ "
						if j == len(shown)-1 {
							branch = "└ "
						}
						out = append(out, in+"  "+stRule.Render(branch)+marker(c, open)+" "+call(c, w-6))
					}
					continue
				}
				for _, c := range it.calls { // a closed run still shows what failed
					if c.err != "" {
						out = append(out, in+"  "+bad.Render("✗")+" "+call(c, w-4))
					}
				}
			}
		}
		if !last { // the turn's end; a compaction or a new run says so
			out = append(out, "")
			if ends[ti].text != "" {
				out = append(out, stRule.Render(truncate("── "+ends[ti].text, n)), "")
			}
		}
	}
	return out
}

// toolSummary is a run's line: its marker, how many calls of which tools, and how many failed.
func toolSummary(calls []tailCall, open bool, n int) string {
	var names []string
	count := map[string]int{}
	failed, waiting := 0, false
	for _, c := range calls {
		if count[c.name] == 0 {
			names = append(names, c.name)
		}
		count[c.name]++
		if c.err != "" {
			failed++
		}
		if !c.done {
			waiting = true
		}
	}
	for i, nm := range names {
		if count[nm] > 1 {
			names[i] = fmt.Sprintf("%s ×%d", nm, count[nm])
		}
	}
	mark := lipgloss.NewStyle().Foreground(colSuccess).Render("•")
	if waiting && open {
		mark = stMuted.Render("◦")
	}
	parts := []seg{{fmt.Sprintf("%d tools · ", len(calls)), stMuted}, {strings.Join(names, ", "), lipgloss.NewStyle().Foreground(colTool)}}
	if failed > 0 {
		parts = append(parts, seg{fmt.Sprintf(" · %d failed", failed), lipgloss.NewStyle().Foreground(colError)})
	}
	return mark + " " + segs(n-2, parts...)
}
