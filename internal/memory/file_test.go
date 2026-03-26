//go:build !windows

package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

// fakeExtractor is a test double for LLMExtractor.
type fakeExtractor struct {
	result string
	err    error
	calls  int
}

func (f *fakeExtractor) ExtractFacts(_ context.Context, _ []model.Message, _ string) (string, error) {
	f.calls++
	return f.result, f.err
}

// makeMessages builds a conversation with n user messages + n assistant messages.
func makeMessages(userCount int) []model.Message {
	var msgs []model.Message
	for i := 0; i < userCount; i++ {
		msgs = append(msgs, model.NewTextMessage(model.RoleUser, "hello"))
		msgs = append(msgs, model.NewTextMessage(model.RoleAssistant, "hi"))
	}
	return msgs
}

func TestFileProvider_BeforeRun_NoFile(t *testing.T) {
	p := NewFileProvider(t.TempDir(), &fakeExtractor{})
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	result, err := p.BeforeRun(context.Background(), rc)
	if err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
}

func TestFileProvider_BeforeRun_WithFile(t *testing.T) {
	dir := t.TempDir()
	p := NewFileProvider(dir, &fakeExtractor{})
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	// Write a MEMORY.md manually.
	scopeDir := filepath.Join(dir, "ns", "agent", "key1")
	os.MkdirAll(scopeDir, 0o755)
	os.WriteFile(filepath.Join(scopeDir, "MEMORY.md"), []byte("- user likes Go\n"), 0o644)

	result, err := p.BeforeRun(context.Background(), rc)
	if err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	if result.Content != "- user likes Go" {
		t.Errorf("expected '- user likes Go', got %q", result.Content)
	}
}

func TestFileProvider_AfterRun_BelowThreshold(t *testing.T) {
	ext := &fakeExtractor{result: "- fact"}
	p := NewFileProvider(t.TempDir(), ext)
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	// 2 user messages < threshold of 3
	msgs := makeMessages(2)
	err := p.AfterRun(context.Background(), rc, msgs)
	if err != nil {
		t.Fatalf("AfterRun: %v", err)
	}
	if ext.calls != 0 {
		t.Errorf("expected 0 extractor calls, got %d", ext.calls)
	}
}

func TestFileProvider_AfterRun_AboveThreshold(t *testing.T) {
	ext := &fakeExtractor{result: "- user prefers Go"}
	dir := t.TempDir()
	p := NewFileProvider(dir, ext)
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	msgs := makeMessages(3)
	err := p.AfterRun(context.Background(), rc, msgs)
	if err != nil {
		t.Fatalf("AfterRun: %v", err)
	}
	if ext.calls != 1 {
		t.Errorf("expected 1 extractor call, got %d", ext.calls)
	}

	// Verify MEMORY.md was written.
	data, err := os.ReadFile(filepath.Join(dir, "ns", "agent", "key1", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if got := string(data); got != "- user prefers Go\n" {
		t.Errorf("MEMORY.md content = %q, want %q", got, "- user prefers Go\n")
	}
}

func TestFileProvider_AfterRun_ExtractNONE(t *testing.T) {
	ext := &fakeExtractor{result: "NONE"}
	dir := t.TempDir()
	p := NewFileProvider(dir, ext)
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	msgs := makeMessages(3)
	err := p.AfterRun(context.Background(), rc, msgs)
	if err != nil {
		t.Fatalf("AfterRun: %v", err)
	}

	// No MEMORY.md should be written.
	memPath := filepath.Join(dir, "ns", "agent", "key1", "MEMORY.md")
	if _, err := os.Stat(memPath); !os.IsNotExist(err) {
		t.Errorf("expected MEMORY.md to not exist when extraction returns NONE")
	}
}

func TestFileProvider_AfterRun_MergesExisting(t *testing.T) {
	ext := &fakeExtractor{result: "- new fact"}
	dir := t.TempDir()
	p := NewFileProvider(dir, ext)
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	// Pre-existing memory.
	scopeDir := filepath.Join(dir, "ns", "agent", "key1")
	os.MkdirAll(scopeDir, 0o755)
	os.WriteFile(filepath.Join(scopeDir, "MEMORY.md"), []byte("- old fact\n"), 0o644)

	msgs := makeMessages(3)
	if err := p.AfterRun(context.Background(), rc, msgs); err != nil {
		t.Fatalf("AfterRun: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(scopeDir, "MEMORY.md"))
	expected := "- old fact\n- new fact\n"
	if got := string(data); got != expected {
		t.Errorf("MEMORY.md = %q, want %q", got, expected)
	}
}

func TestFileProvider_AfterRun_NoAssistantMessages(t *testing.T) {
	ext := &fakeExtractor{result: "- fact"}
	p := NewFileProvider(t.TempDir(), ext)
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}

	// 3 user messages but no assistant messages.
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
		model.NewTextMessage(model.RoleUser, "world"),
		model.NewTextMessage(model.RoleUser, "test"),
	}
	err := p.AfterRun(context.Background(), rc, msgs)
	if err != nil {
		t.Fatalf("AfterRun: %v", err)
	}
	if ext.calls != 0 {
		t.Errorf("expected 0 extractor calls for no assistant messages, got %d", ext.calls)
	}
}

func TestFileProvider_ScopeIsolation(t *testing.T) {
	ext := &fakeExtractor{result: "- scoped fact"}
	dir := t.TempDir()
	p := NewFileProvider(dir, ext)

	rc1 := RunContext{Namespace: "ns1", AgentID: "agent", SessionKey: "key1"}
	rc2 := RunContext{Namespace: "ns2", AgentID: "agent", SessionKey: "key1"}

	msgs := makeMessages(3)
	p.AfterRun(context.Background(), rc1, msgs)
	ext.result = "- other fact"
	p.AfterRun(context.Background(), rc2, msgs)

	data1, _ := os.ReadFile(filepath.Join(dir, "ns1", "agent", "key1", "MEMORY.md"))
	data2, _ := os.ReadFile(filepath.Join(dir, "ns2", "agent", "key1", "MEMORY.md"))

	if string(data1) == string(data2) {
		t.Error("expected different memory for different namespaces")
	}
}

func TestFileProvider_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	rc := RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key1"}
	msgs := makeMessages(3)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ext := &fakeExtractor{result: "- concurrent fact"}
			p := NewFileProvider(dir, ext)
			_ = p.AfterRun(context.Background(), rc, msgs)
		}(i)
	}
	wg.Wait()

	// Verify MEMORY.md exists and is valid — every line should start with "- ".
	data, err := os.ReadFile(filepath.Join(dir, "ns", "agent", "key1", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		t.Error("expected non-empty MEMORY.md after concurrent writes")
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "- ") {
			t.Errorf("corrupt line in MEMORY.md: %q", line)
		}
	}
}

func TestBuildScopeKey_Invalid(t *testing.T) {
	tests := []struct {
		name string
		rc   RunContext
	}{
		{"empty namespace", RunContext{Namespace: "", AgentID: "a", SessionKey: "k"}},
		{"empty agentID", RunContext{Namespace: "ns", AgentID: "", SessionKey: "k"}},
		{"empty sessionKey", RunContext{Namespace: "ns", AgentID: "a", SessionKey: ""}},
		{"path traversal dotdot", RunContext{Namespace: "..", AgentID: "a", SessionKey: "k"}},
		{"slash in namespace", RunContext{Namespace: "ns/bad", AgentID: "a", SessionKey: "k"}},
		{"dotdot prefix", RunContext{Namespace: "../escape", AgentID: "a", SessionKey: "k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildScopeKey(tt.rc)
			if err == nil {
				t.Error("expected error for invalid RunContext")
			}
		})
	}
}

func TestBuildScopeKey_Valid(t *testing.T) {
	tests := []struct {
		name string
		rc   RunContext
	}{
		{"simple", RunContext{Namespace: "ns", AgentID: "agent", SessionKey: "key"}},
		{"dots in name", RunContext{Namespace: "foo..bar", AgentID: "agent", SessionKey: "key"}},
		{"hyphen", RunContext{Namespace: "my-ns", AgentID: "my-agent", SessionKey: "my-key"}},
		{"underscore", RunContext{Namespace: "ns_1", AgentID: "agent_2", SessionKey: "key_3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := buildScopeKey(tt.rc)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if key == "" {
				t.Error("expected non-empty scope key")
			}
		})
	}
}

func TestShouldExtract(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []model.Message
		expected bool
	}{
		{
			"below threshold",
			makeMessages(2),
			false,
		},
		{
			"at threshold",
			makeMessages(3),
			true,
		},
		{
			"above threshold",
			makeMessages(5),
			true,
		},
		{
			"no assistant",
			[]model.Message{
				model.NewTextMessage(model.RoleUser, "a"),
				model.NewTextMessage(model.RoleUser, "b"),
				model.NewTextMessage(model.RoleUser, "c"),
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldExtract(tt.msgs); got != tt.expected {
				t.Errorf("shouldExtract = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMergeMemory(t *testing.T) {
	tests := []struct {
		name      string
		existing  string
		extracted string
		want      string
	}{
		{"empty both", "", "", ""},
		{"new only", "", "- fact", "- fact"},
		{"existing only", "- old", "NONE", "- old"},
		{"merge", "- old", "- new", "- old\n- new"},
		{"empty extracted", "- old", "", "- old"},
		{"none case insensitive", "- old", "none", "- old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeMemory(tt.existing, tt.extracted)
			if got != tt.want {
				t.Errorf("mergeMemory(%q, %q) = %q, want %q", tt.existing, tt.extracted, got, tt.want)
			}
		})
	}
}
