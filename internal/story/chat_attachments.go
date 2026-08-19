package story

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"showmethestory/internal/fsutil"
	"showmethestory/internal/llm"
)

// Attachment limits (decoded byte sizes).
const (
	MaxAttachmentBytes        = 8 << 20  // 单文件 ≤ 8MB
	MaxAttachmentsPerMessage  = 5        // 每次 ≤ 5 个文件
	MaxAttachmentsTotalBytes  = 20 << 20 // 总量 ≤ 20MB
	maxInjectedTextRunes      = 20000    // 文本文件注入 prompt 的内容上限
)

// ChatAttachmentInput is a single uploaded file carried in the chat POST body
// (base64-encoded).
type ChatAttachmentInput struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
}

func attachmentExtAndOK(name, mime string) (string, bool) {
	switch strings.ToLower(mime) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	case "text/plain":
		return ".txt", true
	case "text/markdown":
		return ".md", true
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return ".jpg", true
	case ".png":
		return ".png", true
	case ".gif":
		return ".gif", true
	case ".webp":
		return ".webp", true
	case ".txt":
		return ".txt", true
	case ".md", ".markdown":
		return ".md", true
	}
	return "", false
}

func isImageAttachment(att ChatAttachment) bool {
	return strings.HasPrefix(strings.ToLower(att.Type), "image/")
}

// SaveChatAttachments validates, decodes and persists uploaded files under
// sessions/{sessionID}/attachments/. It returns the persisted references with
// Path relative to the sessions base dir.
func SaveChatAttachments(baseDir, sessionID string, inputs []ChatAttachmentInput) ([]ChatAttachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > MaxAttachmentsPerMessage {
		return nil, fmt.Errorf("一次最多附加 %d 个文件", MaxAttachmentsPerMessage)
	}
	if !isValidSessionID(sessionID) {
		return nil, fmt.Errorf("无效的会话ID")
	}

	dir := filepath.Join(baseDir, sessionID, "attachments")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建附件目录失败: %w", err)
	}

	var total int
	var atts []ChatAttachment
	for _, in := range inputs {
		ext, ok := attachmentExtAndOK(in.Name, in.Type)
		if !ok {
			return nil, fmt.Errorf("不支持的附件类型: %s (%s)", in.Name, in.Type)
		}
		if in.Data == "" {
			return nil, fmt.Errorf("附件内容为空: %s", in.Name)
		}
		data, err := base64.StdEncoding.DecodeString(in.Data)
		if err != nil {
			return nil, fmt.Errorf("附件解码失败: %s", in.Name)
		}
		if len(data) > MaxAttachmentBytes {
			return nil, fmt.Errorf("附件 %s 超过 %dMB 大小限制", in.Name, MaxAttachmentBytes>>20)
		}
		total += len(data)
		if total > MaxAttachmentsTotalBytes {
			return nil, fmt.Errorf("附件总大小超过 %dMB 限制", MaxAttachmentsTotalBytes>>20)
		}

		rel := filepath.Join(sessionID, "attachments", randomAttachmentName()+ext)
		if err := fsutil.WriteFile(filepath.Join(baseDir, rel), data); err != nil {
			return nil, fmt.Errorf("保存附件失败: %s", in.Name)
		}
		atts = append(atts, ChatAttachment{Name: in.Name, Type: in.Type, Path: rel})
	}
	return atts, nil
}

// ChatAttachmentAbsPath resolves an attachment reference to an absolute path,
// guarding against path traversal. References are always stored as
// {sessionID}/attachments/{file} relative to the sessions base dir.
func ChatAttachmentAbsPath(baseDir string, att ChatAttachment) (string, error) {
	if att.Path == "" {
		return "", fmt.Errorf("无效的附件路径")
	}
	rel := filepath.Clean(att.Path)
	if rel != att.Path || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("无效的附件路径")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[1] != "attachments" || parts[0] == "" || parts[2] == "" {
		return "", fmt.Errorf("附件路径越界")
	}
	return filepath.Join(baseDir, rel), nil
}

// BuildAttachmentContentParts reads persisted attachments and converts them
// into OpenAI-compatible content blocks: images become base64 data-URL
// image_url blocks, text files become text blocks (content capped).
func BuildAttachmentContentParts(baseDir string, atts []ChatAttachment) ([]llm.ContentPart, error) {
	var parts []llm.ContentPart
	for _, att := range atts {
		abs, err := ChatAttachmentAbsPath(baseDir, att)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("读取附件失败: %w", err)
		}
		if isImageAttachment(att) {
			dataURL := "data:" + att.Type + ";base64," + base64.StdEncoding.EncodeToString(data)
			parts = append(parts, llm.ImageContentPart(dataURL))
			continue
		}
		text := truncateRunes(string(data), maxInjectedTextRunes)
		parts = append(parts, llm.TextContentPart(fmt.Sprintf("【附件：%s】\n%s", att.Name, text)))
	}
	return parts, nil
}

// DeleteSessionAttachments removes the attachments directory of a session.
func DeleteSessionAttachments(baseDir, sessionID string) error {
	if !isValidSessionID(sessionID) {
		return fmt.Errorf("无效的会话ID")
	}
	dir := filepath.Join(baseDir, sessionID, "attachments")
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func randomAttachmentName() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("a_%d", time.Now().UnixNano())
	}
	return "a_" + hex.EncodeToString(b)
}