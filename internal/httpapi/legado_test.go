package httpapi

import "testing"

func TestChapterDisplayTitle(t *testing.T) {
	cases := []struct {
		num   int
		title string
		lang  string
		want  string
	}{
		{1, "旧外套的口袋", "zh", "第1章 旧外套的口袋"},
		{42, "海堤上的证词", "zh", "第42章 海堤上的证词"},
		{1, "Into the Fog", "en", "Chapter 1: Into the Fog"},
		{3, "", "zh", "第3章"},
		{2, "", "en", "Chapter 2"},
		// 已带序号 → 不重复加前缀
		{1, "第一章 序篇", "zh", "第一章 序篇"},
		{5, "第 12 章 往事", "zh", "第 12 章 往事"},
		{2, "Chapter 3: The Drop", "en", "Chapter 3: The Drop"},
		{7, "第100章 尾声", "en", "第100章 尾声"}, // 中文前缀在 project 语言为 en 时也保留
	}
	for _, c := range cases {
		if got := chapterDisplayTitle(c.num, c.title, c.lang); got != c.want {
			t.Errorf("chapterDisplayTitle(%d, %q, %q) = %q, want %q", c.num, c.title, c.lang, got, c.want)
		}
	}
}

func TestHasChapterPrefix(t *testing.T) {
	for _, s := range []string{
		"第一章 序篇", "第1章 x", "第 3 章 往事", "第20章", "chapter one",
		"Chapter 5: A", "CHAPTER 100", "第百章",
	} {
		if !hasChapterPrefix(s) {
			t.Errorf("expected prefix for %q", s)
		}
	}
	for _, s := range []string{"旧外套", "严树", "The Drop", "1. Spring", "前言"} {
		if hasChapterPrefix(s) {
			t.Errorf("did NOT expect prefix for %q", s)
		}
	}
}
