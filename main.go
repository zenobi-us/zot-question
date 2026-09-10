package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/patriceckhart/zot/packages/agent/ext"
)

const name = "zot-question"

// version is overridden by release builds with -ldflags; local builds use the
// version declared in extension.json.
var version = "0.1.0"

const schema = `{"type":"object","properties":{"title":{"type":"string","description":"Short decision title"},"intro":{"type":"string","description":"Context shown above the questions"},"questions":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string","enum":["choice","text"]},"header":{"type":"string"},"prompt":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"},"details":{"type":"string"}},"required":["value","label"]}},"multi":{"type":"boolean"},"recommendation":{} ,"placeholder":{"type":"string"}},"required":["id","type","header","prompt"]}}},"required":["questions"]}`

type option struct {
	Value, Label, Description, Details string
	Selected                           bool
	Comment                            string
}
type question struct {
	ID, Type, Header, Prompt, Placeholder, Recommendation string
	Multi, Answered                                       bool
	Options                                               []option
	Text, Comment                                         string
}
type params struct {
	Title, Intro string
	Questions    []struct {
		ID, Type, Header, Prompt, Placeholder string
		Options                               []struct{ Value, Label, Description, Details string }
		Multi                                 bool
		Recommendation                        any `json:"recommendation"`
	}
}

type form struct {
	title, intro string
	questions    []question
	cursor       int
	mode         string // answer, review, comment, option-comment
	comment      string
	option       int
	result       chan ext.ToolResult
	once         sync.Once
	e            *ext.Extension
}

func main() {
	e := ext.New(name, version)
	e.Tool("ask_user", "Ask the user one focused decision using a structured keyboard-driven form.", json.RawMessage(schema), func(raw json.RawMessage) ext.ToolResult {
		var in params
		if err := json.Unmarshal(raw, &in); err != nil {
			return ext.TextErrorResult("invalid ask_user arguments: " + err.Error())
		}
		f, err := newForm(e, in)
		if err != nil {
			return ext.TextErrorResult(err.Error())
		}
		f.open()
		return <-f.result
	})
	if err := e.Run(); err != nil {
		e.Logf("fatal: %v", err)
		os.Exit(1)
	}
}

func newForm(e *ext.Extension, in params) (*form, error) {
	if len(in.Questions) < 1 || len(in.Questions) > 10 {
		return nil, fmt.Errorf("ask_user supports 1-10 questions (got %d)", len(in.Questions))
	}
	f := &form{e: e, title: strings.TrimSpace(in.Title), intro: strings.TrimSpace(in.Intro), mode: "answer", result: make(chan ext.ToolResult, 1)}
	seen := map[string]bool{}
	for _, q := range in.Questions {
		q.ID, q.Header, q.Prompt = strings.TrimSpace(q.ID), strings.TrimSpace(q.Header), strings.TrimSpace(q.Prompt)
		if q.ID == "" || q.Header == "" || q.Prompt == "" {
			return nil, fmt.Errorf("each question requires non-empty id, header, and prompt")
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		item := question{ID: q.ID, Type: q.Type, Header: q.Header, Prompt: q.Prompt, Placeholder: strings.TrimSpace(q.Placeholder), Multi: q.Multi}
		switch q.Type {
		case "text":
			if s, ok := q.Recommendation.(string); ok {
				item.Text = s
				item.Recommendation = s
			}
		case "choice":
			if len(q.Options) < 2 || len(q.Options) > 12 {
				return nil, fmt.Errorf("choice question %q requires 2-12 options", q.ID)
			}
			for _, o := range q.Options {
				if strings.TrimSpace(o.Value) == "" || strings.TrimSpace(o.Label) == "" {
					return nil, fmt.Errorf("choice question %q has an empty option", q.ID)
				}
				item.Options = append(item.Options, option{Value: strings.TrimSpace(o.Value), Label: strings.TrimSpace(o.Label), Description: o.Description, Details: o.Details})
			}
			if q.Multi {
				if values, ok := q.Recommendation.([]any); ok {
					for _, v := range values {
						f.selectValue(&item, fmt.Sprint(v))
					}
				}
			} else if s, ok := q.Recommendation.(string); ok {
				item.Recommendation = s
				f.selectValue(&item, s)
			}
			if !q.Multi && !hasSelection(item.Options) {
				item.Options[0].Selected = true
			}
		default:
			return nil, fmt.Errorf("question %q has unsupported type %q", q.ID, q.Type)
		}
		f.questions = append(f.questions, item)
	}
	return f, nil
}

func hasSelection(options []option) bool {
	for _, o := range options {
		if o.Selected {
			return true
		}
	}
	return false
}
func (f *form) selectValue(q *question, value string) {
	for i := range q.Options {
		if q.Options[i].Value == strings.TrimSpace(value) {
			q.Options[i].Selected = true
			return
		}
	}
}
func (f *form) pid() string { return fmt.Sprintf("ask-user-%p", f) }
func (f *form) open() {
	pid := f.pid()
	finish := func(r ext.ToolResult) { f.once.Do(func() { f.e.ClosePanel(pid); f.result <- r }) }
	f.e.OnPanelKey(pid, func(key, text string) { f.key(pid, key, text, finish) }, func() { finish(ext.TextErrorResult("ask_user cancelled: the form was closed")) })
	f.e.OpenPanel(pid, f.panelTitle(), f.lines(), f.footer())
}
func (f *form) redraw(pid string) { f.e.RenderPanel(pid, f.panelTitle(), f.lines(), f.footer()) }
func (f *form) key(pid, key, text string, finish func(ext.ToolResult)) {
	if f.mode == "comment" || f.mode == "option-comment" {
		f.commentKey(pid, key, text)
		return
	}
	if f.mode == "review" {
		f.reviewKey(pid, key, text, finish)
		return
	}
	q := &f.questions[f.cursor]
	switch key {
	case "up":
		f.move(-1)
	case "down":
		f.move(1)
	case "left":
		if f.cursor > 0 {
			f.cursor--
		}
	case "right", "tab":
		if f.cursor < len(f.questions)-1 {
			f.cursor++
		} else {
			f.mode = "review"
		}
	case "backtab":
		if f.cursor > 0 {
			f.cursor--
		}
	case "space":
		if q.Type == "choice" {
			if q.Multi {
				q.Options[f.option].Selected = !q.Options[f.option].Selected
			} else {
				for i := range q.Options {
					q.Options[i].Selected = i == f.option
				}
			}
		}
	case "enter":
		f.acceptAnswer()
	case "backspace":
		if q.Type == "text" {
			r := []rune(q.Text)
			if len(r) > 0 {
				q.Text = string(r[:len(r)-1])
			}
		}
	case "rune":
		switch strings.ToLower(text) {
		case "c":
			f.mode = "comment"
			f.comment = q.Comment
		case "n":
			if q.Type == "choice" {
				f.mode = "option-comment"
				f.comment = q.Options[f.option].Comment
			}
		case "u":
			q.Answered = false
			f.next()
		default:
			if q.Type == "text" {
				q.Text += text
			}
		}
	}
	f.redraw(pid)
}
func (f *form) move(delta int) {
	q := &f.questions[f.cursor]
	if q.Type == "choice" {
		f.option += delta
		if f.option < 0 {
			f.option = len(q.Options) - 1
		}
		if f.option >= len(q.Options) {
			f.option = 0
		}
	} else if delta != 0 {
		if delta > 0 && f.cursor < len(f.questions)-1 {
			f.cursor++
		}
		if delta < 0 && f.cursor > 0 {
			f.cursor--
		}
	}
}
func (f *form) acceptAnswer() {
	q := &f.questions[f.cursor]
	q.Answered = true
	if q.Type == "text" && strings.TrimSpace(q.Text) == "" {
		q.Answered = false
	}
	f.next()
}
func (f *form) next() {
	if f.cursor < len(f.questions)-1 {
		f.cursor++
		f.option = 0
	} else {
		f.mode = "review"
	}
}
func (f *form) commentKey(pid, key, text string) {
	switch key {
	case "backspace":
		r := []rune(f.comment)
		if len(r) > 0 {
			f.comment = string(r[:len(r)-1])
		}
	case "enter":
		q := &f.questions[f.cursor]
		if f.mode == "comment" {
			q.Comment = strings.TrimSpace(f.comment)
		} else {
			q.Options[f.option].Comment = strings.TrimSpace(f.comment)
		}
		f.comment = ""
		f.mode = "answer"
	case "esc":
		f.comment = ""
		f.mode = "answer"
	case "rune":
		f.comment += text
	}
	f.redraw(pid)
}
func (f *form) reviewKey(pid, key, text string, finish func(ext.ToolResult)) {
	switch key {
	case "left":
		f.mode = "answer"
		f.cursor = len(f.questions) - 1
	case "up":
		f.move(-1)
	case "down":
		f.move(1)
	case "enter":
		finish(f.resultValue())
	case "rune":
		switch strings.ToLower(text) {
		case "e":
			f.mode = "answer"
			f.cursor = f.cursor % len(f.questions)
		case "u":
			f.questions[f.cursor].Answered = false
		}
	case "esc":
		finish(ext.TextErrorResult("ask_user cancelled by user"))
	}
	f.redraw(pid)
}

func (f *form) panelTitle() string {
	if f.title != "" {
		return "Ask User — " + f.title
	}
	return "Ask User"
}
func (f *form) lines() []string {
	if f.mode == "review" {
		return f.reviewLines()
	}
	q := f.questions[f.cursor]
	lines := []string{fmt.Sprintf("  %d/%d  %s", f.cursor+1, len(f.questions), q.Header), "", "  " + q.Prompt, ""}
	if f.intro != "" && f.cursor == 0 {
		lines = append([]string{"  " + f.intro, ""}, lines...)
	}
	if q.Type == "text" {
		value := q.Text
		if value == "" && q.Placeholder != "" {
			value = "[" + q.Placeholder + "]"
		}
		lines = append(lines, "  "+value+"▌")
	} else {
		for i, o := range q.Options {
			mark := "○"
			if o.Selected {
				mark = "●"
			}
			if q.Multi {
				mark = "☑"
				if !o.Selected {
					mark = "☐"
				}
			}
			cursor := "  "
			if i == f.option {
				cursor = "› "
			}
			lines = append(lines, cursor+mark+" "+o.Label)
			if i == f.option && (o.Description != "" || o.Details != "") {
				lines = append(lines, "    "+o.Description, "    "+o.Details)
			}
		}
	}
	if q.Comment != "" {
		lines = append(lines, "", "  Comment: "+q.Comment)
	}
	return lines
}
func (f *form) reviewLines() []string {
	lines := []string{"  Review your answers", ""}
	if f.intro != "" {
		lines = append(lines, "  "+f.intro, "")
	}
	for i, q := range f.questions {
		mark := "✓"
		answer := f.answerText(q)
		if !q.Answered {
			mark = "?"
			answer = "unanswered"
		}
		cursor := "  "
		if i == f.cursor {
			cursor = "› "
		}
		lines = append(lines, fmt.Sprintf("%s%s %s: %s", cursor, mark, q.Header, answer))
	}
	if f.comment != "" {
		lines = append(lines, "", "  Form comment: "+f.comment)
	}
	return lines
}
func (f *form) answerText(q question) string {
	if q.Type == "text" {
		return q.Text
	}
	var a []string
	for _, o := range q.Options {
		if o.Selected {
			a = append(a, o.Label)
		}
	}
	return strings.Join(a, ", ")
}
func (f *form) footer() string {
	if f.mode == "review" {
		return "↑/↓ review · e edit · enter submit · esc cancel"
	}
	if f.mode == "comment" || f.mode == "option-comment" {
		return "type comment · enter save · esc cancel"
	}
	return "↑/↓ move · space select · enter next · tab next · c comment · u unanswered · esc cancel"
}

func (f *form) resultValue() ext.ToolResult {
	outcome := "submitted"
	for _, q := range f.questions {
		if !q.Answered {
			outcome = "needs_discussion"
		}
	}
	var b strings.Builder
	if outcome == "needs_discussion" {
		b.WriteString("User needs discussion before a complete decision.\n")
	}
	for _, q := range f.questions {
		if q.Answered {
			fmt.Fprintf(&b, "%s: %s\n", q.Header, f.answerText(q))
		} else {
			fmt.Fprintf(&b, "%s: unanswered\n", q.Header)
		}
		if q.Comment != "" {
			fmt.Fprintf(&b, "%s comment: %s\n", q.Header, q.Comment)
		}
		for _, o := range q.Options {
			if o.Comment != "" {
				fmt.Fprintf(&b, "%s option comment (%s): %s\n", q.Header, o.Label, o.Comment)
			}
		}
	}
	return ext.TextResult(b.String())
}
