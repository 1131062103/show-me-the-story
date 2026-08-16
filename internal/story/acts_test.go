package story

import (
	"context"
	"showmethestory/internal/config"
	"showmethestory/internal/i18n"
	"strings"
	"testing"
)

func TestAssignRangesUneven(t *testing.T) {
	// 1200 chapters, 7 volumes, unequal split summing exactly to 1200.
	r := assignRanges([]int{180, 200, 200, 200, 200, 200, 20}, 1, 1200)
	if len(r) != 7 {
		t.Fatalf("expected 7 arcs, got %d", len(r))
	}
	if r[0].Start != 1 || r[0].End != 180 {
		t.Fatalf("first arc wrong: %+v", r[0])
	}
	if r[1].Start != 181 || r[1].End != 380 {
		t.Fatalf("second arc wrong: %+v", r[1])
	}
	if r[6].Start != 1181 || r[6].End != 1200 {
		t.Fatalf("last arc wrong: %+v", r[6])
	}
	// Ranges must be contiguous with no gaps.
	for i := 1; i < len(r); i++ {
		if r[i].Start != r[i-1].End+1 {
			t.Fatalf("gap between arc %d and %d: %+v", i-1, i, r)
		}
	}

	// Drift: last arc absorbs the difference (must equal the exact total).
	r = assignRanges([]int{10, 20}, 1, 35)
	if r[1].End != 35 {
		t.Fatalf("positive drift not absorbed: %+v", r)
	}
	r = assignRanges([]int{20, 20}, 1, 30)
	if r[1].End != 30 {
		t.Fatalf("negative drift not absorbed: %+v", r)
	}
	// Zero counts get a minimum of 1.
	r = assignRanges([]int{0, 5}, 5, 10)
	if r[0].Start != 5 || r[0].End != 5 || r[1].End != 14 {
		t.Fatalf("zero count handling wrong: %+v", r)
	}
}

func TestActBeforeChapterNum(t *testing.T) {
	state := &Progress{
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
	// Chapter in act 2 of arc 1: previous act is act 1 of arc 1.
	if prev := actBeforeChapterNum(state, 6); prev == nil || prev.Title != "幕一" {
		t.Fatalf("expected 幕一 as previous act, got %+v", prev)
	}
	// First act of the book: no previous.
	if actBeforeChapterNum(state, 2) != nil {
		t.Fatal("first act must have no previous act")
	}
	// First act of arc 2: previous act is the last act of arc 1.
	if prev := actBeforeChapterNum(state, 10); prev == nil || prev.Title != "幕二" {
		t.Fatalf("expected 幕二 (last act of prior arc) as previous, got %+v", prev)
	}
	// Legacy project without acts: nil.
	legacy := &Progress{Arcs: []Arc{{ID: 1, StartCh: 1, EndCh: 8}}}
	if actBeforeChapterNum(legacy, 3) != nil {
		t.Fatal("legacy arc with no acts must yield nil previous act")
	}
}

func TestActSummaryGateBlocksIncompletePrev(t *testing.T) {
	dir := t.TempDir()
	state := &Progress{
		Phase:   "writing",
		BookOverview: "整书概览",
		Arcs: []Arc{
			{ID: 1, StartCh: 1, EndCh: 6, Acts: []Act{
				{ID: 1, Title: "幕一", StartCh: 1, EndCh: 3},
				{ID: 2, Title: "幕二", StartCh: 4, EndCh: 6, ChaptersConfirmed: true},
			}},
		},
		Chapters: []ChapterState{
			{Num: 1, Status: StatusAccepted, Summary: "s1"},
			{Num: 2, Status: StatusAccepted, Summary: "s2"},
			{Num: 3, Status: StatusAccepted, Summary: "s3"},
			{Num: 4, Status: StatusPending, Outline: "o4"},
		},
		CurrentChapterIndex: 3,
	}
	apiCfg := &config.APIConfig{BaseURL: "http://localhost", Model: "test-model"}
	err := GenerateChapterAction(context.Background(), apiCfg, &config.Config{}, state, dir, &ProjectSettings{}, nil)
	if err == nil {
		t.Fatal("expected the act-summary gate to block writing")
	}
	if !strings.Contains(err.Error(), "幕摘要") {
		t.Fatalf("gate error should mention 幕摘要: %v", err)
	}
	// After the summary exists, the gate passes (API errors out later, not at gate).
	state.Arcs[0].Acts[0].Summary = "幕一摘要"
	err = GenerateChapterAction(context.Background(), apiCfg, &config.Config{}, state, dir, &ProjectSettings{}, nil)
	if err == nil || strings.Contains(err.Error(), "幕摘要") {
		t.Fatalf("gate should no longer fire once summary exists: %v", err)
	}
}

func TestActLookupAcrossArcs(t *testing.T) {
	state := &Progress{
		Arcs: []Arc{
			{ID: 1, StartCh: 1, EndCh: 8, Acts: []Act{
				{ID: 1, StartCh: 1, EndCh: 4},
				{ID: 2, StartCh: 5, EndCh: 8},
			}},
			{ID: 2, StartCh: 9, EndCh: 12, Acts: []Act{
				{ID: 1, StartCh: 9, EndCh: 12},
			}},
		},
	}
	if act := actForChapterNum(state, 6); act == nil || act.ID != 2 || act.StartCh != 5 {
		t.Fatalf("actForChapterNum wrong: %+v", act)
	}
	if act := actForChapterNum(state, 10); act == nil || act.ID != 1 {
		t.Fatalf("act in second arc wrong: %+v", act)
	}
	if actForChapterNum(state, 99) != nil {
		t.Fatal("expected nil for out-of-range chapter")
	}
	// Legacy project (no acts): must not match anything.
	legacy := &Progress{
		Arcs: []Arc{{ID: 1, StartCh: 1, EndCh: 8}},
	}
	if actForChapterNum(legacy, 3) != nil {
		t.Fatal("legacy arc with no acts must yield nil act")
	}
}

func TestBuildPreviousActContext(t *testing.T) {
	state := &Progress{
		Arcs: []Arc{
			{ID: 1, Title: "开端", StartCh: 1, EndCh: 4, Acts: []Act{
				{ID: 1, Title: "序幕", StartCh: 1, EndCh: 2, Outline: "序幕幕纲"},
				{ID: 2, Title: "展开", StartCh: 3, EndCh: 4},
			}},
			{ID: 2, Title: "终局", StartCh: 5, EndCh: 6},
		},
		Chapters: []ChapterState{
			{Num: 1, Summary: "ch1"},
			{Num: 2, Summary: "ch2"},
		},
	}
	// Generating act 2 of arc 1 (chapters 3-4): should include the previous
	// act's outline plus the tail chapter summary before the act.
	got := buildPreviousActContext(state, 0, 1, i18n.LangZH)
	if !strings.Contains(got, "序幕幕纲") {
		t.Fatalf("missing previous act outline: %s", got)
	}
	if !strings.Contains(got, "ch2") {
		t.Fatalf("missing tail chapter summary: %s", got)
	}

	// Opening act of the book: fixed placeholder.
	empty := buildPreviousActContext(&Progress{Arcs: []Arc{{ID: 1, Title: "A", StartCh: 1, EndCh: 4, Acts: []Act{{ID: 1, Title: "a", StartCh: 1, EndCh: 4}}}}}, 0, 0, i18n.LangZH)
	if !strings.Contains(empty, "故事开端") {
		t.Fatalf("opening placeholder missing: %s", empty)
	}
}

func TestBuildFutureActsBlock(t *testing.T) {
	state := &Progress{
		Arcs: []Arc{
			{ID: 1, Title: "开端", StartCh: 1, EndCh: 8, Acts: []Act{
				{ID: 1, Title: "幕一", StartCh: 1, EndCh: 4, Goal: "幕一目标"},
				{ID: 2, Title: "幕二", StartCh: 5, EndCh: 8, Goal: "幕二目标"},
			}},
			{ID: 2, Title: "终局", StartCh: 9, EndCh: 12, Goal: "终局目标"},
		},
	}
	got := buildFutureActsBlock(state, 0, 0, i18n.LangZH)
	if !strings.Contains(got, "幕二") || !strings.Contains(got, "终局") {
		t.Fatalf("later act/arc missing: %s", got)
	}
	if strings.Contains(got, "幕一") {
		t.Fatalf("current act should not be listed: %s", got)
	}
	final := buildFutureActsBlock(state, 1, 0, i18n.LangZH)
	if !strings.Contains(final, "最后一幕") {
		t.Fatalf("final act placeholder missing: %s", final)
	}
}

func TestEditActRecomputesRanges(t *testing.T) {
	state := &Progress{
		Arcs: []Arc{
			{ID: 1, StartCh: 1, EndCh: 6, Acts: []Act{
				{ID: 1, Title: "a", StartCh: 1, EndCh: 2},
				{ID: 2, Title: "b", StartCh: 3, EndCh: 6},
			}},
		},
	}
	if err := EditActAction(state, 1, 1, "", "", 4); err != nil {
		t.Fatalf("edit act failed: %v", err)
	}
	acts := state.Arcs[0].Acts
	if acts[0].EndCh-acts[0].StartCh+1 != 4 {
		t.Fatalf("first act width not updated: %+v", acts[0])
	}
	// Volume range must stay contiguous 1..6.
	if acts[0].StartCh != 1 || acts[1].EndCh != 6 || acts[0].EndCh+1 != acts[1].StartCh {
		t.Fatalf("ranges not contiguous after edit: %+v", acts)
	}
	// Locked act: chapter count changes are rejected.
	state2 := &Progress{Arcs: []Arc{{ID: 1, StartCh: 1, EndCh: 6, Acts: []Act{
		{ID: 1, StartCh: 1, EndCh: 2, Confirmed: true},
	}}}}
	if err := EditActAction(state2, 1, 1, "", "", 5); err == nil {
		t.Fatal("expected error editing confirmed act")
	}
}

func TestConfirmActChaptersMovesPhaseToWriting(t *testing.T) {
	dir := t.TempDir()
	progressPath := dir + "/progress.json"
	state := &Progress{
		Phase:    "outline",
		Arcs:     []Arc{{ID: 1, StartCh: 1, EndCh: 2, Acts: []Act{{ID: 1, StartCh: 1, EndCh: 2}}}},
		Chapters: []ChapterState{
			{Num: 1, Status: StatusPending, Outline: "大纲1"},
			{Num: 2, Status: StatusPending, Outline: "大纲2"},
		},
	}
	if err := ConfirmActChaptersAction(state, progressPath, 1, 1); err != nil {
		t.Fatalf("confirm act chapters failed: %v", err)
	}
	if !state.Arcs[0].Acts[0].ChaptersConfirmed {
		t.Fatal("act chapters not marked confirmed")
	}
	if state.Phase != "writing" {
		t.Fatalf("phase should move to writing, got %q", state.Phase)
	}
	// Act with a chapter lacking outline must be rejected.
	state2 := &Progress{
		Phase:    "outline",
		Arcs:     []Arc{{ID: 1, StartCh: 1, EndCh: 2, Acts: []Act{{ID: 1, StartCh: 1, EndCh: 2}}}},
		Chapters: []ChapterState{{Num: 1, Status: StatusPending, Outline: ""}},
	}
	if err := ConfirmActChaptersAction(state2, progressPath, 1, 1); err == nil {
		t.Fatal("expected error when a chapter outline is missing")
	}
}