package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageContentSerialization(t *testing.T) {
	// 纯文本消息仍序列化为字符串（向后兼容）。
	plain, err := json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), `"content":"hi"`) {
		t.Fatalf("plain content should serialize as string, got %s", plain)
	}

	// 多部分消息序列化为数组。
	msg := Message{Role: "user", Content: []ContentPart{
		TextContentPart("看下这张图"),
		ImageContentPart("data:image/png;base64,AAAA"),
	}}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(encoded)
	if !strings.Contains(s, `"content":[{"type":"text","text":"看下这张图"}`) {
		t.Fatalf("expected text part first, got %s", s)
	}
	if !strings.Contains(s, `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`) {
		t.Fatalf("expected image_url part, got %s", s)
	}
}

func TestContentRuneLen(t *testing.T) {
	if n := ContentRuneLen("hello"); n != 5 {
		t.Fatalf("string len = %d", n)
	}
	parts := []ContentPart{TextContentPart("abc"), ImageContentPart("data:image/png;base64,x")}
	if n := ContentRuneLen(parts); n != 3+500 {
		t.Fatalf("parts len = %d", n)
	}
}
