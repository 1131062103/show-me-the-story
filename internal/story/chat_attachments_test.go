package story

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndBuildChatAttachments(t *testing.T) {
	base := t.TempDir()
	img := base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10})
	txt := base64.StdEncoding.EncodeToString([]byte("hello 设定文件"))

	atts, err := SaveChatAttachments(base, "s_1", []ChatAttachmentInput{
		{Name: "cover.png", Type: "image/png", Data: img},
		{Name: "设定.md", Type: "text/markdown", Data: txt},
	})
	if err != nil {
		t.Fatalf("SaveChatAttachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(atts))
	}
	for _, a := range atts {
		abs, err := ChatAttachmentAbsPath(base, a)
		if err != nil {
			t.Fatalf("ChatAttachmentAbsPath(%s): %v", a.Path, err)
		}
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("attachment not on disk: %v", err)
		}
		if !strings.HasPrefix(a.Path, "s_1/attachments/") {
			t.Fatalf("unexpected path %q", a.Path)
		}
	}

	parts, err := BuildAttachmentContentParts(base, atts)
	if err != nil {
		t.Fatalf("BuildAttachmentContentParts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "image_url" || parts[0].ImageURL == nil || !strings.HasPrefix(parts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image part wrong: %+v", parts[0])
	}
	if parts[1].Type != "text" || !strings.Contains(parts[1].Text, "设定文件") {
		t.Fatalf("text part wrong: %+v", parts[1])
	}
}

func TestSaveChatAttachmentsRejects(t *testing.T) {
	base := t.TempDir()
	ok := base64.StdEncoding.EncodeToString([]byte("x"))

	// empty input is a no-op
	if atts, err := SaveChatAttachments(base, "s_1", nil); err != nil || atts != nil {
		t.Fatalf("empty input should be a no-op: %v", err)
	}

	// unsupported type
	if _, err := SaveChatAttachments(base, "s_1", []ChatAttachmentInput{{Name: "a.pdf", Type: "application/pdf", Data: ok}}); err == nil {
		t.Fatalf("expected unsupported type rejection")
	}
	// bad base64
	if _, err := SaveChatAttachments(base, "s_1", []ChatAttachmentInput{{Name: "a.png", Type: "image/png", Data: "!!!"}}); err == nil {
		t.Fatalf("expected decode rejection")
	}
	// too many files
	var many []ChatAttachmentInput
	for i := 0; i < MaxAttachmentsPerMessage+1; i++ {
		many = append(many, ChatAttachmentInput{Name: "a.png", Type: "image/png", Data: ok})
	}
	if _, err := SaveChatAttachments(base, "s_1", many); err == nil {
		t.Fatalf("expected count rejection")
	}
	// oversized single file
	big := base64.StdEncoding.EncodeToString(make([]byte, MaxAttachmentBytes+1))
	if _, err := SaveChatAttachments(base, "s_1", []ChatAttachmentInput{{Name: "a.png", Type: "image/png", Data: big}}); err == nil {
		t.Fatalf("expected size rejection")
	}
	// invalid session id
	if _, err := SaveChatAttachments(base, "../evil", []ChatAttachmentInput{{Name: "a.png", Type: "image/png", Data: ok}}); err == nil {
		t.Fatalf("expected session id rejection")
	}
}

func TestChatAttachmentAbsPathGuardsTraversal(t *testing.T) {
	base := t.TempDir()
	bad := []ChatAttachment{
		{Name: "x", Path: "../secret"},
		{Name: "x", Path: "/etc/passwd"},
		{Name: "x", Path: "s_1/attachments"},
		{Name: "x", Path: "s_1/attachments/.."},
	}
	for _, a := range bad {
		if _, err := ChatAttachmentAbsPath(base, a); err == nil {
			t.Fatalf("expected traversal rejection for %q", a.Path)
		}
	}
}

func TestDeleteSessionAttachments(t *testing.T) {
	base := t.TempDir()
	atts, err := SaveChatAttachments(base, "s_2", []ChatAttachmentInput{{Name: "a.png", Type: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("y"))}})
	if err != nil {
		t.Fatalf("SaveChatAttachments: %v", err)
	}
	dir := filepath.Dir(mustAbs(t, base, atts[0]))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("attachments dir missing: %v", err)
	}
	if err := DeleteSessionAttachments(base, "s_2"); err != nil {
		t.Fatalf("DeleteSessionAttachments: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("attachments dir should be gone, got %v", err)
	}
}

func mustAbs(t *testing.T, base string, att ChatAttachment) string {
	t.Helper()
	abs, err := ChatAttachmentAbsPath(base, att)
	if err != nil {
		t.Fatalf("ChatAttachmentAbsPath: %v", err)
	}
	return abs
}