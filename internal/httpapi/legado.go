package httpapi

// Legado (开源阅读) 书源适配层。
//
// 让未改动的 Legado 客户端能通过「网络导入书源」直接阅读本项目的全部小说：
//   1. 用户在 show-me-the-story 任选访问途径（局域网 IPv4/IPv6、域名、Cloudflare
//      Tunnel 等）后，把 `<base>/api/legado/book-source.json` 粘贴进 Legado 的
//      「网络导入书源」。
//   2. Legado 拉取该 JSON，得到一套指向本项目后端接口的书源规则（searchUrl、
//      ruleSearch / ruleBookInfo / ruleToc / ruleContent，全部使用 JSONPath）。
//   3. 之后 Legado 通过搜索 / 获取目录 / 拉正文，全部由本项目返回 JSON 数据，
//      Legado 自身零改造。
//
// 关键约定（与 Legado AnalyzeRule 引擎一致）：
//   - 所有规则写成 JSONPath（“$.x” / “@Json:” 前缀），无论响应 Content-Type 或
//     正文形态如何都强制按 JSON 解析，稳。
//   - 所有回写进 JSON 正文的 bookUrl / chapterUrl 都拼成**绝对地址**，基准取
//     当前请求的 Host（+ X-Forwarded-Proto 反代头），因此无需手动配置可达地址，
//     只要用户用哪个地址访问，返回的数据就落在该地址上。
//   - 无论章节是否完稿，content 都照常返回，Legado 侧统一可读。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"showmethestory/internal/story"
)

// baseURL derives the externally-reachable origin for URL construction from the
// incoming request, honoring reverse-proxy proto headers. Host always resolves
// to the address the client actually used to reach us.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

// projectNames returns the directory names under storysDir(), sorted.
func (h *Handlers) projectNames() []string {
	entries, err := os.ReadDir(h.storysDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// loadProject loads a project's progress (incl. chapter prose) by directory name.
func (h *Handlers) loadProject(name string) (*story.Progress, error) {
	dir := filepath.Join(h.storysDir(), name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("项目不存在: %s", name)
	}
	return story.LoadProgress(filepath.Join(dir, "progress.json"))
}

// encPath URL-encodes a path segment (project names may contain CJK / spaces).
func encPath(s string) string {
	return url.PathEscape(s)
}

// —— 书源定义导出 ——

// GetLegadoBookSource serves the BookSource JSON a user pastes into Legado's
// 「网络导入书源」. All URLs inside are built from the request's own Host, so the
// same endpoint works over LAN IP, IPv6, domain, or Cloudflare tunnel untouched.
func (h *Handlers) GetLegadoBookSource(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	searchURL := base + "/api/legado/search?q={{key}}&page={{page}}"

	source := map[string]any{
		"bookSourceUrl":  base,
		"bookSourceName": "Show Me The Story（本地AI小说）",
		"bookSourceGroup": "本地",
		"bookSourceType": 0,
		"enabled":        true,
		"enabledExplore": false,
		"enabledReview":  false,
		"exploreUrl":     "",
		"searchUrl":      searchURL,
		"ruleSearch": map[string]any{
			"bookList":    "@Json:$.books",
			"name":        "$.name",
			"author":      "$.author",
			"intro":       "$.intro",
			"kind":        "$.kind",
			"lastChapter": "$.last_chapter",
			"bookUrl":     "$.bookUrl",
			"coverUrl":    "$.cover",
			"hasMoreRule": "false",
		},
		"ruleBookInfo": map[string]any{
			"name":        "$.name",
			"author":      "$.author",
			"intro":       "$.intro",
			"kind":        "$.kind",
			"lastChapter": "$.last_chapter",
			"wordCount":   "$.word_count",
			"coverUrl":    "$.cover",
			"tocUrl":      "",
		},
		"ruleToc": map[string]any{
			"chapterList": "@Json:$.chapters",
			"chapterName": "$.title",
			"chapterUrl":  "$.url",
		},
		"ruleContent": map[string]any{
			"content": "$.content",
		},
		"enabledCookieJar": false,
	}

	// 纯文本书源格式：加 UTF-8 BOM 规避个别导入路径按 GBK 猜测导致乱码。
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(source)
}

// —— 搜索 ——

// GetLegadoSearch lists books (projects) whose title / summary / synopsis match
// the keyword. Returns the JSON object that ruleSearch.bookList selects.
func (h *Handlers) GetLegadoSearch(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	type book struct {
		Name        string `json:"name"`
		Author      string `json:"author"`
		Intro       string `json:"intro"`
		Kind        string `json:"kind"`
		LastChapter string `json:"last_chapter"`
		Cover       string `json:"cover"`
		BookURL     string `json:"bookUrl"`
	}
	books := []book{}

	for _, name := range h.projectNames() {
		p, err := h.loadProject(name)
		if err != nil || p == nil || p.Title == "" {
			continue
		}
		if keyword != "" {
			hay := strings.ToLower(p.Title + " " + p.StorySynopsis + " " + p.CorePrompt)
			if !strings.Contains(hay, keyword) {
				continue
			}
		}

		books = append(books, book{
			Name:        p.Title,
			Author:      "Show Me The Story",
			Intro:       firstLine(p.StorySynopsis),
			Kind:        kindLabel(p),
			LastChapter: lastChapterTitle(p),
			Cover:       "",
			BookURL:     base + "/api/legado/book/" + encPath(name),
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"books": books})
}

// kindLabel gives a rough genre tag from the story config snapshot if available.
func kindLabel(p *story.Progress) string {
	if p.StoryConfigSnapshot != nil {
		if s := strings.TrimSpace(p.StoryConfigSnapshot.Type); s != "" {
			return s
		}
	}
	return "小说"
}

// firstLine returns the first non-empty line, trimmed, or "".
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// lastChapterTitle returns the title of the highest-numbered chapter.
func lastChapterTitle(p *story.Progress) string {
	title := ""
	maxNum := 0
	for _, ch := range p.Chapters {
		if ch.Num > maxNum {
			maxNum = ch.Num
			title = ch.Title
		}
	}
	return title
}

// projectLanguage reads the project config's language ("zh"/"en"), defaulting to
// zh for legacy projects. Used to chapter the TOC labels.
func (h *Handlers) projectLanguage(proj string) string {
	data, err := os.ReadFile(filepath.Join(h.storysDir(), proj, "config.json"))
	if err != nil {
		return "zh"
	}
	var cfg struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "zh"
	}
	if strings.EqualFold(cfg.Language, "en") {
		return "en"
	}
	return "zh"
}

// chapterDisplayTitle returns the chapter title prefixed with its ordinal, e.g.
// 「第一章 旧外套的口袋」 for zh or "Chapter 1: Old coat pockets" for en.
// Skips the prefix entirely when the stored title already carries one, so we
// never double-prefix a legacy title like "第一章 序篇" or "第 3 章 ...".
func chapterDisplayTitle(num int, title, lang string) string {
	t := strings.TrimSpace(title)
	if hasChapterPrefix(t) {
		return t
	}
	if lang == "en" {
		if t == "" {
			return fmt.Sprintf("Chapter %d", num)
		}
		return fmt.Sprintf("Chapter %d: %s", num, t)
	}
	if t == "" {
		return fmt.Sprintf("第%d章", num)
	}
	return fmt.Sprintf("第%d章 %s", num, t)
}

// hasChapterPrefix reports whether title already begins with a chapter marker
// such as "第1章", "第一章", "第 42 章" (zh) or "Chapter N" / "chapter n" (en).
func hasChapterPrefix(title string) bool {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "chapter ") || strings.HasPrefix(lower, "chapter:") {
		return true
	}
	// 第…章：可含可选空格再跟中文或数字。
	rest := strings.TrimPrefix(t, "第")
	rest = strings.TrimSpace(rest)
	// 第X章 形态
	if i := strings.Index(rest, "章"); i > 0 {
		seg := rest[:i]
		if allChapterDigitsOrCN(seg) {
			return true
		}
	}
	return false
}

// allChapterDigitsOrCN reports whether the segment between 第 and 章 looks like a
// chapter number (arabic digits / 中文数字，含可能的空格与分隔符).
func allChapterDigitsOrCN(seg string) bool {
	if seg == "" {
		return false
	}
	digits := "0123456789零一二三四五六七八九十百千两"
	for _, r := range seg {
		if r == ' ' || r == '-' || r == '、' || r == '_' {
			continue
		}
		if !strings.ContainsRune(digits, r) {
			return false
		}
	}
	return true
}

// ——— 书籍信息 + 目录（同源返回，一次请求） ———

// GetLegadoBook returns a single book's metadata plus its full chapter list.
// Legado applies ruleBookInfo then (tocUrl empty ⇒ falls back to the same URL
// and reuses the cached body) ruleToc against this single response.
func (h *Handlers) GetLegadoBook(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	proj := r.PathValue("project")

	p, err := h.loadProject(proj)
	if err != nil || p == nil {
		http.NotFound(w, r)
		return
	}

	type chapter struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	lang := h.projectLanguage(proj)
	chapters := []chapter{}
	wordCount := 0
	for _, ch := range p.Chapters {
		if ch.Num <= 0 {
			continue
		}
		chapters = append(chapters, chapter{
			Title: chapterDisplayTitle(ch.Num, ch.Title, lang),
			URL:   base + "/api/legado/chapter/" + encPath(proj) + "/" + fmt.Sprintf("%d", ch.Num),
		})
		wordCount += ch.WordCount
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"name":         p.Title,
		"author":       "Show Me The Story",
		"intro":        p.StorySynopsis,
		"kind":         kindLabel(p),
		"word_count":   wordCount,
		"last_chapter": lastChapterTitle(p),
		"cover":        "",
		"chapters":     chapters,
	})
}

// —— 正文 ——

// GetLegadoChapter returns a single chapter's prose for the content rule.
func (h *Handlers) GetLegadoChapter(w http.ResponseWriter, r *http.Request) {
	proj := r.PathValue("project")
	var num int
	if _, err := fmt.Sscanf(r.PathValue("num"), "%d", &num); err != nil {
		http.Error(w, "invalid chapter num", http.StatusBadRequest)
		return
	}

	p, err := h.loadProject(proj)
	if err != nil || p == nil {
		http.NotFound(w, r)
		return
	}
	idx := story.FindChapterIdx(p, num)
	if idx < 0 {
		http.NotFound(w, r)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"title":   p.Chapters[idx].Title,
		"content": p.Chapters[idx].Content,
	})
}
