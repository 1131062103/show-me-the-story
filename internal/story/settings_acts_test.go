package story

import (
	"showmethestory/internal/i18n"
	"strings"
	"testing"
)

func TestActScopedCharacterFiltering(t *testing.T) {
	settings := &ProjectSettings{
		Characters: []Character{
			{Name: "主角", Personality: "全局角色"},
			{Name: "幕一专属", Personality: "只在幕一"},
			{Name: "幕二专属", Personality: "只在幕二"},
		},
	}
	settings.Characters[1].Acts = []int{1}
	settings.Characters[2].Acts = []int{2}

	ch := ChapterState{Num: 2, Outline: "主角 幕一专属 幕二专属"}
	// 幕一（全局索引1）：应包含 主角 + 幕一专属，不含幕二专属。
	ctx := buildCharacterContextForLang(settings, ch, i18n.LangZH, 1)
	if !strings.Contains(ctx, "幕一专属") {
		t.Fatalf("act-1 chapter must include act-1 scoped character: %s", ctx)
	}
	if strings.Contains(ctx, "幕二专属") {
		t.Fatalf("act-1 chapter must NOT include act-2 scoped character: %s", ctx)
	}
	if !strings.Contains(ctx, "全局角色") {
		t.Fatalf("global character must always be included: %s", ctx)
	}

	// 幕二（全局索引2）：包含 主角 + 幕二专属。
	ctx = buildCharacterContextForLang(settings, ch, i18n.LangZH, 2)
	if !strings.Contains(ctx, "幕二专属") {
		t.Fatalf("act-2 chapter must include act-2 scoped character: %s", ctx)
	}
	if strings.Contains(ctx, "幕一专属") {
		t.Fatalf("act-2 chapter must NOT include act-1 scoped character: %s", ctx)
	}

	// 非 v4（actID=0）：不过滤，全部注入。
	ctx = buildCharacterContextForLang(settings, ch, i18n.LangZH, 0)
	if !strings.Contains(ctx, "幕一专属") || !strings.Contains(ctx, "幕二专属") {
		t.Fatalf("actID=0 must disable scoping: %s", ctx)
	}
}

func TestActScopedWorldviewFiltering(t *testing.T) {
	settings := &ProjectSettings{
		Worldview: []WorldviewEntry{
			{Name: "全局设定", Description: "d"},
			{Name: "幕一地点", Description: "d1"},
			{Name: "幕二地点", Description: "d2"},
		},
		Organizations: []Organization{
			{Name: "全局组织", Description: "o"},
			{Name: "幕一势力", Description: "o1"},
		},
	}
	settings.Worldview[1].Acts = []int{1}
	settings.Worldview[2].Acts = []int{2}
	settings.Organizations[1].Acts = []int{1}

	outline := "幕一地点 幕二地点 幕一势力 全局组织"
	ctx := buildWorldviewContextForLang(settings, outline, i18n.LangZH, 1)
	if !strings.Contains(ctx, "幕一地点") {
		t.Fatalf("act-1 worldview missing: %s", ctx)
	}
	if strings.Contains(ctx, "幕二地点") {
		t.Fatalf("act-2 worldview leaked into act 1: %s", ctx)
	}
	if !strings.Contains(ctx, "幕一势力") {
		t.Fatalf("act-1 organization missing: %s", ctx)
	}

	ctx = buildWorldviewContextForLang(settings, outline, i18n.LangZH, 2)
	if !strings.Contains(ctx, "幕二地点") {
		t.Fatalf("act-2 worldview missing: %s", ctx)
	}
	if strings.Contains(ctx, "幕一势力") {
		t.Fatalf("act-1 organization leaked into act 2: %s", ctx)
	}
}

func TestActIDForChapterGlobalIndex(t *testing.T) {
	state := &Progress{
		BookOverview: "整书概览",
		Arcs: []Arc{
			{ID: 1, StartCh: 1, EndCh: 8, Acts: []Act{
				{ID: 1, Title: "幕一", StartCh: 1, EndCh: 4},
				{ID: 2, Title: "幕二", StartCh: 5, EndCh: 8},
			}},
			{ID: 2, StartCh: 9, EndCh: 12, Acts: []Act{
				{ID: 1, Title: "幕三", StartCh: 9, EndCh: 12},
			}},
		},
	}
	// 全局索引：幕一=1, 幕二=2, 幕三=3（即使两个卷都有 act.ID=1）。
	if got := actIDForChapter(state, 2); got != 1 {
		t.Fatalf("act ID for ch2 should be global index 1, got %d", got)
	}
	if got := actIDForChapter(state, 6); got != 2 {
		t.Fatalf("act ID for ch6 should be global index 2, got %d", got)
	}
	if got := actIDForChapter(state, 10); got != 3 {
		t.Fatalf("act ID for ch10 should be global index 3, got %d", got)
	}
	// Legacy project: 0 (no scoping).
	legacy := &Progress{Arcs: []Arc{{ID: 1, StartCh: 1, EndCh: 8}}}
	if got := actIDForChapter(legacy, 3); got != 0 {
		t.Fatalf("legacy project must return 0, got %d", got)
	}
}
