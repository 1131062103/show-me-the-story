// Package chat persists assistant chat sessions in the legacy-compatible format.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	ToolResult     string     `json:"tool_result,omitempty"`
	ToolResultKey  string     `json:"tool_result_key,omitempty"`
	ToolResultArgs []string   `json:"tool_result_args,omitempty"`
	Timestamp      string     `json:"timestamp"`
}

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Messages  []Message `json:"messages"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type SessionMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	MsgCount  int    `json:"msg_count"`
}

type Index struct {
	Sessions []SessionMeta `json:"sessions"`
}

type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }
func (s *Store) Dir() string     { return s.dir }

func ValidSessionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	return !strings.ContainsAny(id, `/\\.:`)
}

func (s *Store) LoadIndex(ctx context.Context) (*Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "index.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return &Index{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chat session index: %w", err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode chat session index: %w", err)
	}
	return &index, nil
}

func (s *Store) Load(ctx context.Context, id string) (*Session, error) {
	if !ValidSessionID(id) {
		return nil, errors.New("invalid session ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chat session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("read chat session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("decode chat session: %w", err)
	}
	return &session, nil
}

func (s *Store) Save(ctx context.Context, session *Session) error {
	if session == nil || !ValidSessionID(session.ID) {
		return errors.New("valid chat session is required")
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(ctx, filepath.Join(s.dir, session.ID+".json"), data); err != nil {
		return err
	}
	index, err := s.LoadIndex(ctx)
	if err != nil {
		return err
	}
	found := false
	for i := range index.Sessions {
		if index.Sessions[i].ID == session.ID {
			index.Sessions[i].Title, index.Sessions[i].UpdatedAt, index.Sessions[i].MsgCount = session.Title, session.UpdatedAt, len(session.Messages)
			found = true
		}
	}
	if !found {
		index.Sessions = append(index.Sessions, SessionMeta{ID: session.ID, Title: session.Title, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt, MsgCount: len(session.Messages)})
	}
	data, err = json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(ctx, filepath.Join(s.dir, "index.json"), data)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if !ValidSessionID(id) {
		return errors.New("invalid session ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	index, err := s.LoadIndex(ctx)
	if err != nil {
		return err
	}
	out := index.Sessions[:0]
	for _, item := range index.Sessions {
		if item.ID != id {
			out = append(out, item)
		}
	}
	index.Sessions = out
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(ctx, filepath.Join(s.dir, "index.json"), data)
}

func NewSession(userMessage string) *Session {
	now := time.Now().Format(time.RFC3339Nano)
	return &Session{ID: fmt.Sprintf("s_%d", time.Now().UnixNano()), Title: Title(userMessage), CreatedAt: now, UpdatedAt: now}
}
func Title(message string) string {
	r := []rune(message)
	if len(r) > 20 {
		return string(r[:20]) + "..."
	}
	return message
}

func writeAtomic(ctx context.Context, path string, data []byte) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
