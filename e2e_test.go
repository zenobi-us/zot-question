package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

// TestInteractiveToolE2E exercises the extension over the real stdin/stdout
// protocol. It catches the regression where ask_user is registered as a normal
// tool and therefore receives zot's bounded reply timeout.
func TestInteractiveToolE2E(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = io.ReadAll(stderr)
	})

	frames := make(chan map[string]any, 16)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var frame map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				readErr <- err
				return
			}
			frames <- frame
		}
		readErr <- scanner.Err()
	}()

	next := func(want string) map[string]any {
		t.Helper()
		select {
		case frame := <-frames:
			if got, _ := frame["type"].(string); got != want {
				t.Fatalf("expected %q frame, got %#v", want, frame)
			}
			return frame
		case err := <-readErr:
			t.Fatalf("extension stdout closed while waiting for %q: %v", want, err)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %q frame", want)
		}
		return nil
	}

	hello := next("hello")
	caps := stringSlice(hello["capabilities"])
	if !contains(caps, "tool_cancel") {
		t.Fatalf("hello capabilities do not advertise tool_cancel: %v", caps)
	}
	sendFrame(t, stdin, extproto.HelloAckFromHost{
		Type:            "hello_ack",
		ProtocolVersion: extproto.ProtocolVersion,
		ZotVersion:      "0.3.75",
		Provider:        "test",
		Model:           "test",
	})

	var registered map[string]any
	for {
		frame := next(anyType(next, "register_tool"))
		if frame["name"] == "ask_user" {
			registered = frame
			break
		}
	}
	if interactive, ok := registered["interactive"].(bool); !ok || !interactive {
		t.Fatalf("ask_user was not registered as interactive: %#v", registered)
	}
	next("ready")

	sendFrame(t, stdin, extproto.ToolCallFromHost{
		Type: "tool_call",
		ID:   "e2e-call",
		Name: "ask_user",
		Args: json.RawMessage(`{"title":"Timeout test","questions":[{"id":"answer","type":"text","header":"Answer","prompt":"Type anything"}]}`),
	})
	panel := next("open_panel")
	panelSpec, ok := panel["panel"].(map[string]any)
	if !ok {
		t.Fatalf("open_panel has no panel payload: %#v", panel)
	}
	panelID, _ := panelSpec["id"].(string)
	if panelID == "" {
		t.Fatalf("open_panel has no panel id: %#v", panel)
	}

	// The interactive registration assertion above catches the regression that
	// routes this call through zot's bounded normal-tool timeout. Do not wait
	// for that one-minute boundary here: the test must stay fast and reliable.
	// A short pause still verifies that opening the panel does not complete the
	// tool call before the user provides an answer.
	time.Sleep(100 * time.Millisecond)
	select {
	case frame := <-frames:
		t.Fatalf("interactive call completed before user input: %#v", frame)
	default:
	}

	sendFrame(t, stdin, extproto.PanelKeyFromHost{Type: "panel_key", PanelID: panelID, Key: "rune", Text: "x"})
	next("panel_render")
	sendFrame(t, stdin, extproto.PanelKeyFromHost{Type: "panel_key", PanelID: panelID, Key: "enter"})
	next("panel_close")
	result := next("tool_result")
	if result["id"] != "e2e-call" {
		t.Fatalf("tool result has wrong id: %#v", result)
	}
}

func sendFrame(t *testing.T, w io.Writer, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func contains(values []string, want string) bool {
	return strings.Join(values, "\x00") != "" && strings.Contains("\x00"+strings.Join(values, "\x00")+"\x00", "\x00"+want+"\x00")
}

// anyType keeps the frame loop readable while still making the expected type
// explicit at the call site.
func anyType(_ func(string) map[string]any, want string) string { return want }
