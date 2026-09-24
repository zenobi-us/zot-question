package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestIntroLinesRenderMarkdownAndWrap(t *testing.T) {
	t.Setenv("COLUMNS", "32")
	f := &form{intro: "# Context\n\nChoose **carefully** from these options.\n\n- first\n- second"}

	lines := f.introLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Context") {
		t.Fatalf("intro heading was not rendered: %q", joined)
	}
	if !strings.Contains(joined, "carefully") {
		t.Fatalf("intro body was not rendered: %q", joined)
	}
	if strings.Contains(joined, "**carefully**") {
		t.Fatalf("raw Markdown leaked into intro: %q", joined)
	}
	if len(lines) < 6 {
		t.Fatalf("expected Markdown paragraphs and list to occupy multiple lines, got %d: %q", len(lines), joined)
	}
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for i, line := range lines {
		if len([]rune(ansi.ReplaceAllString(line, ""))) > 32 {
			t.Errorf("line %d exceeds terminal width: %d runes: %q", i, len([]rune(ansi.ReplaceAllString(line, ""))), line)
		}
	}
}
