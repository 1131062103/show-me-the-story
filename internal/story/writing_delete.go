package story

import (
	"errors"
	"fmt"
	"showmethestory/internal/fsutil"
	"showmethestory/internal/i18n"
	"strings"
)

var (
	ErrNoChaptersToDelete         = errors.New("no chapters to delete")
	ErrWritingChapterCannotDelete = errors.New("writing chapter cannot delete")
	ErrDeleteFrontierUnavailable  = errors.New("delete frontier unavailable")
	ErrReviewRejectUnavailable    = errors.New("review reject unavailable")
)

// ResolveDeleteChapterTarget returns the chapter index eligible for delete_chapter:
// the writing-frontier chapter — review/writing at CurrentChapterIndex, or the last
// accepted chapter when the next slot is still pending, or the final chapter when
// the book is fully confirmed.
func ResolveDeleteChapterTarget(state *Progress) (int, error) {
	if len(state.Chapters) == 0 {
		return -1, ErrNoChaptersToDelete
	}

	frontier := state.CurrentChapterIndex
	if frontier >= len(state.Chapters) {
		last := len(state.Chapters) - 1
		if state.Chapters[last].Status == StatusWriting {
			return -1, ErrWritingChapterCannotDelete
		}
		if chapterHasDeletableContent(&state.Chapters[last]) {
			return last, nil
		}
		return -1, ErrDeleteFrontierUnavailable
	}

	cur := &state.Chapters[frontier]
	switch cur.Status {
	case StatusWriting:
		return -1, ErrWritingChapterCannotDelete
	case StatusReview:
		return frontier, nil
	case StatusPending:
		if frontier == 0 {
			return -1, ErrDeleteFrontierUnavailable
		}
		prev := &state.Chapters[frontier-1]
		if prev.Status == StatusAccepted && chapterHasDeletableContent(prev) {
			return frontier - 1, nil
		}
		return -1, ErrDeleteFrontierUnavailable
	default:
		return -1, ErrDeleteFrontierUnavailable
	}
}

func chapterHasDeletableContent(ch *ChapterState) bool {
	switch ch.Status {
	case StatusAccepted, StatusReview:
		return true
	default:
		return ch.Content != "" || ch.Summary != ""
	}
}

func purgeMemoryForChapter(state *Progress, chapterNum int) {
	filtered := state.MemoryEntries[:0]
	for _, m := range state.MemoryEntries {
		if m.Chapter != chapterNum {
			filtered = append(filtered, m)
		}
	}
	state.MemoryEntries = filtered
}

func clearChapterContentAt(state *Progress, projectDir string, idx int) {
	ch := &state.Chapters[idx]
	fsutil.Delete(ChapterMarkdownPath(projectDir, ch.Num))
	ch.Content = ""
	ch.Summary = ""
	ch.Status = StatusPending
	ch.Finalized = false
	purgeMemoryForChapter(state, ch.Num)
}

func adjustCurrentChapterIndexAfterDelete(state *Progress, deletedIdx int) {
	if deletedIdx < state.CurrentChapterIndex {
		state.CurrentChapterIndex = deletedIdx
	} else if state.CurrentChapterIndex >= len(state.Chapters) {
		state.CurrentChapterIndex = deletedIdx
	}
}

// DeleteFrontierChapter clears prose at the writing frontier (see ResolveDeleteChapterTarget).
func DeleteFrontierChapter(state *Progress, projectDir string) (int, error) {
	idx, err := ResolveDeleteChapterTarget(state)
	if err != nil {
		return 0, err
	}
	num := state.Chapters[idx].Num
	clearChapterContentAt(state, projectDir, idx)
	adjustCurrentChapterIndexAfterDelete(state, idx)
	return num, nil
}

// RejectChapterAction 审核驳回：仅当写作指针处的章节正等待用户确认（review）时可用。
// 驳回即清除该章正文与该章记忆（含被推迟生成的摘要/记忆占位），并把写作指针留在本章，
// 便于用户重新生成。与 DeleteFrontierChapter（写作回退）的区别在于它只对 review 审核章
// 生效，语义上是对「审核」流程的明示否定，而非通用的前沿正文清除。
func RejectChapterAction(state *Progress, projectDir string) (int, error) {
	idx := state.CurrentChapterIndex
	if idx < 0 || idx >= len(state.Chapters) {
		return 0, ErrNoChaptersToDelete
	}
	ch := &state.Chapters[idx]
	if ch.Status != StatusReview {
		return 0, ErrReviewRejectUnavailable
	}
	num := ch.Num
	clearChapterContentAt(state, projectDir, idx)
	state.CurrentChapterIndex = idx
	return num, nil
}

func FormatWritingFrontierInfo(state *Progress, lang string) string {
	if state.Phase != "writing" || len(state.Chapters) == 0 {
		return ""
	}

	zh := i18n.NormalizeLanguage(lang) == i18n.LangZH
	var sb strings.Builder
	if frontier := state.CurrentChapterIndex; frontier < len(state.Chapters) {
		ch := state.Chapters[frontier]
		if zh {
			sb.WriteString(fmt.Sprintf("写作指针: 第 %d 章 [%s]\n", ch.Num, ch.Status))
		} else {
			sb.WriteString(fmt.Sprintf("Writing pointer: chapter %d [%s]\n", ch.Num, ch.Status))
		}
	} else if zh {
		sb.WriteString("写作指针: 全书章节已确认完毕\n")
	} else {
		sb.WriteString("Writing pointer: all chapters confirmed\n")
	}

	if idx, err := ResolveDeleteChapterTarget(state); err == nil {
		num := state.Chapters[idx].Num
		if zh {
			sb.WriteString(fmt.Sprintf("delete_chapter 当前可删: 第 %d 章（写作前沿）\n", num))
		} else {
			sb.WriteString(fmt.Sprintf("delete_chapter can remove: chapter %d (writing frontier)\n", num))
		}
	} else if zh {
		sb.WriteString("delete_chapter 当前不可用（如无正文可删或正在写作中）\n")
	} else {
		sb.WriteString("delete_chapter unavailable (nothing to delete or a chapter is writing)\n")
	}
	return sb.String()
}
