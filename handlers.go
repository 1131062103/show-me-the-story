package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type Handlers struct {
	apiCfg     *APIConfig
	apiCfgPath string
	cfg        *Config
	cfgPath    string
	state      *Progress
	progressPath string
	logger     *LogBroadcaster
	taskMu     sync.Mutex
	taskRunning bool

	pendingContinueContent string
}

func NewHandlers(apiCfg *APIConfig, apiCfgPath string, cfg *Config, cfgPath string, state *Progress, progressPath string, logger *LogBroadcaster) *Handlers {
	return &Handlers{
		apiCfg:       apiCfg,
		apiCfgPath:   apiCfgPath,
		cfg:          cfg,
		cfgPath:      cfgPath,
		state:        state,
		progressPath: progressPath,
		logger:       logger,
	}
}

func (h *Handlers) writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (h *Handlers) writeError(w http.ResponseWriter, code int, msg string) {
	h.writeJSON(w, code, map[string]string{"error": msg})
}

func (h *Handlers) tryStartTask() bool {
	h.taskMu.Lock()
	defer h.taskMu.Unlock()
	if h.taskRunning {
		return false
	}
	h.taskRunning = true
	return true
}

func (h *Handlers) endTask() {
	h.taskMu.Lock()
	h.taskRunning = false
	h.taskMu.Unlock()
}

func (h *Handlers) isTaskRunning() bool {
	h.taskMu.Lock()
	defer h.taskMu.Unlock()
	return h.taskRunning
}

func (h *Handlers) GetAPIConfig(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, h.apiCfg)
}

func (h *Handlers) PutAPIConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg APIConfig
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	if newCfg.HTTPTimeoutSeconds <= 0 {
		newCfg.HTTPTimeoutSeconds = 300
	}

	data, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "序列化API配置失败: "+err.Error())
		return
	}
	if err := writeFileAtomic(h.apiCfgPath, data); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存API配置失败: "+err.Error())
		return
	}

	h.apiCfg = &newCfg
	h.writeJSON(w, http.StatusOK, h.apiCfg)
}

func (h *Handlers) GetConfig(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, h.cfg)
}

func (h *Handlers) PutConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg Config
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	if newCfg.Story.ChapterCount <= 0 {
		newCfg.Story.ChapterCount = 30
	}
	if newCfg.Story.TargetWordsPerChapter <= 0 {
		newCfg.Story.TargetWordsPerChapter = 2500
	}
	newCfg.Prompts.applyDefaults()

	data, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "序列化配置失败: "+err.Error())
		return
	}
	if err := writeFileAtomic(h.cfgPath, data); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}

	h.cfg = &newCfg
	h.writeJSON(w, http.StatusOK, h.cfg)
}

func (h *Handlers) GetProgress(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) DeleteProgress(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，无法重置进度")
		return
	}

	if err := deleteFile(h.progressPath); err != nil {
		h.writeError(w, http.StatusInternalServerError, "删除进度文件失败: "+err.Error())
		return
	}

	h.state = &Progress{Phase: "outline"}
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PostOutlineGenerate(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	go func() {
		h.logger.TaskStart("outline_generation")

		h.logger.Info("正在生成小说大纲...")
		err := GenerateOutlineAction(h.apiCfg, h.cfg, h.state, h.progressPath, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("大纲生成失败: %v", err))
			h.logger.TaskEnd("outline_generation", false)
			return
		}

		h.endTask()
		h.logger.Success("大纲生成完成！")
		h.logger.TaskEnd("outline_generation", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) PostOutlineConfirm(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	if h.state.Phase != "outline" {
		h.writeError(w, http.StatusBadRequest, "当前不在大纲阶段")
		return
	}

	if len(h.state.Chapters) == 0 {
		h.writeError(w, http.StatusBadRequest, "大纲为空，请先生成大纲")
		return
	}

	if err := ConfirmOutlineAction(h.state, h.progressPath); err != nil {
		h.writeError(w, http.StatusInternalServerError, "确认大纲失败: "+err.Error())
		return
	}

	h.logger.Success("大纲已确认，进入写作阶段。")
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PostOutlineRevise(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	var body struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Feedback == "" {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "缺少 feedback 字段")
		return
	}

	go func() {
		h.logger.TaskStart("outline_revision")

		h.logger.Info("正在根据意见修订大纲...")
		err := ReviseOutlineAction(h.apiCfg, h.cfg, h.state, h.progressPath, body.Feedback, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("大纲修订失败: %v", err))
			h.logger.TaskEnd("outline_revision", false)
			return
		}

		h.endTask()
		h.logger.Success("大纲已修订。")
		h.logger.TaskEnd("outline_revision", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) PostChapterGenerate(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	go func() {
		h.logger.TaskStart("chapter_generation")

		chIdx := h.state.CurrentChapterIndex
		chTitle := ""
		if chIdx < len(h.state.Chapters) {
			chTitle = h.state.Chapters[chIdx].Title
		}

		h.logger.Info(fmt.Sprintf("正在创作第 %d 章...", chIdx+1))
		err := GenerateChapterAction(h.apiCfg, h.cfg, h.state, h.progressPath, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("章节创作失败: %v", err))
			h.logger.TaskEnd("chapter_generation", false)
			return
		}

		h.endTask()
		h.logger.Success(fmt.Sprintf("第 %d 章《%s》创作完成！", chIdx+1, chTitle))
		h.logger.TaskEnd("chapter_generation", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) PostChapterConfirm(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	if h.state.Phase != "writing" {
		h.writeError(w, http.StatusBadRequest, "当前不在写作阶段")
		return
	}

	if err := ConfirmChapterAction(h.state, h.progressPath); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ch := h.state.Chapters[h.state.CurrentChapterIndex-1]
	h.logger.Success(fmt.Sprintf("第 %d 章已确认。", ch.Num))
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PostChapterRevise(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	var body struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Feedback == "" {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "缺少 feedback 字段")
		return
	}

	go func() {
		h.logger.TaskStart("chapter_revision")

		h.logger.Info("正在根据意见修改当前章节...")
		err := ReviseChapterAction(h.apiCfg, h.cfg, h.state, h.progressPath, body.Feedback, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("章节修订失败: %v", err))
			h.logger.TaskEnd("chapter_revision", false)
			return
		}

		h.endTask()
		h.logger.Success("章节已修订。")
		h.logger.TaskEnd("chapter_revision", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) DeleteChapter(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，无法删除章节")
		return
	}

	if len(h.state.Chapters) == 0 {
		h.writeError(w, http.StatusBadRequest, "没有可删除的章节")
		return
	}

	lastIdx := len(h.state.Chapters) - 1
	ch := h.state.Chapters[lastIdx]

	if ch.Status == StatusWriting {
		h.writeError(w, http.StatusConflict, "正在写作中的章节无法删除")
		return
	}

	mdFile := fmt.Sprintf("Chapter_%02d.md", ch.Num)
	deleteFile(mdFile)

	h.state.Chapters = h.state.Chapters[:lastIdx]

	if h.state.CurrentChapterIndex > len(h.state.Chapters) {
		h.state.CurrentChapterIndex = len(h.state.Chapters)
	}

	if len(h.state.Chapters) == 0 {
		h.state.Phase = "outline"
		h.state.CurrentChapterIndex = 0
		h.state.StoryConfigSnapshot = nil
	}

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存进度失败: "+err.Error())
		return
	}

	h.logger.Success(fmt.Sprintf("已删除第 %d 章。", ch.Num))
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) DeleteOutline(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，无法删除大纲")
		return
	}

	for _, ch := range h.state.Chapters {
		if ch.Status == StatusWriting || ch.Status == StatusReview {
			h.writeError(w, http.StatusConflict, "有正在写作/审核中的章节，请先处理后再删除大纲")
			return
		}
	}

	h.state.Title = ""
	h.state.CorePrompt = ""
	h.state.CoreRequirements = ""
	h.state.Chapters = nil
	h.state.StoryConfigSnapshot = nil
	h.state.CurrentChapterIndex = 0

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存进度失败: "+err.Error())
		return
	}

	h.logger.Success("大纲已删除。")
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PutChapterOutline(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	numStr := r.PathValue("num")
	var num int
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的章节编号")
		return
	}

	var body struct {
		Title   string `json:"title"`
		Outline string `json:"outline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	if err := EditChapterOutline(h.state, num, body.Title, body.Outline); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存进度失败: "+err.Error())
		return
	}

	h.logger.Success(fmt.Sprintf("第 %d 章大纲已更新。", num))
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PostSettingsReconcile(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	var body StoryConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	go func() {
		h.logger.TaskStart("settings_reconciliation")

		h.logger.Info("正在协调新设定与已有内容...")
		err := ReconcileSettingsAction(h.apiCfg, h.cfg, h.state, body, h.progressPath, h.cfgPath, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("设定协调失败: %v", err))
			h.logger.TaskEnd("settings_reconciliation", false)
			return
		}

		h.endTask()
		h.logger.Success("设定协调完成！")
		h.logger.TaskEnd("settings_reconciliation", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) DeleteChaptersFrom(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，无法删除章节")
		return
	}

	numStr := r.PathValue("num")
	var num int
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的章节编号")
		return
	}

	startIdx := -1
	for i, ch := range h.state.Chapters {
		if ch.Num == num {
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		h.writeError(w, http.StatusNotFound, fmt.Sprintf("章节 %d 不存在", num))
		return
	}

	for i := startIdx; i < len(h.state.Chapters); i++ {
		if h.state.Chapters[i].Status == StatusWriting {
			h.writeError(w, http.StatusConflict, "删除范围内有正在写作中的章节，无法删除")
			return
		}
	}

	deletedCount := len(h.state.Chapters) - startIdx

	for i := startIdx; i < len(h.state.Chapters); i++ {
		mdFile := fmt.Sprintf("Chapter_%02d.md", h.state.Chapters[i].Num)
		if err := deleteFile(mdFile); err != nil {
			h.logger.Warn(fmt.Sprintf("删除文件 %s 失败: %v", mdFile, err))
		}
	}

	h.state.Chapters = h.state.Chapters[:startIdx]

	if h.state.CurrentChapterIndex > len(h.state.Chapters) {
		h.state.CurrentChapterIndex = len(h.state.Chapters)
	}

	if len(h.state.Chapters) == 0 {
		h.state.Phase = "outline"
		h.state.CurrentChapterIndex = 0
		h.state.StoryConfigSnapshot = nil
	}

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存进度失败: "+err.Error())
		return
	}

	h.logger.Success(fmt.Sprintf("已从第 %d 章删除到末尾，共删除 %d 章。", num, deletedCount))
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) broadcastProgress() {
	accepted := 0
	for _, ch := range h.state.Chapters {
		if ch.Status == StatusAccepted {
			accepted++
		}
	}
	total := len(h.state.Chapters)
	var pct float64
	if total > 0 {
		pct = float64(accepted) / float64(total) * 100
	}
	h.logger.ProgressUpdate(map[string]interface{}{
		"phase":            h.state.Phase,
		"title":            h.state.Title,
		"current_chapter":  h.state.CurrentChapterIndex,
		"total_chapters":   total,
		"accepted_chapters": accepted,
		"percent":          pct,
		"is_task_running":  h.isTaskRunning(),
	})
}

func (h *Handlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"phase":           h.state.Phase,
		"title":           h.state.Title,
		"total_chapters":  len(h.state.Chapters),
		"is_task_running": h.isTaskRunning(),
	})
}

func (h *Handlers) GetForeshadows(w http.ResponseWriter, r *http.Request) {
	if h.state.Foreshadows == nil {
		h.writeJSON(w, http.StatusOK, []Foreshadow{})
		return
	}
	h.writeJSON(w, http.StatusOK, h.state.Foreshadows)
}

func (h *Handlers) PostForeshadowsSuggest(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	if len(h.state.Chapters) == 0 {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "请先生成大纲")
		return
	}

	go func() {
		h.logger.TaskStart("foreshadow_suggest")

		h.logger.Info("正在分析大纲，设计伏笔方案...")
		suggestions, err := SuggestForeshadows(h.apiCfg, h.cfg, h.state, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("伏笔建议生成失败: %v", err))
			h.logger.TaskEnd("foreshadow_suggest", false)
			return
		}

		h.endTask()
		h.logger.Success(fmt.Sprintf("伏笔建议生成完成，共 %d 条", len(suggestions)))
		h.logger.TaskEnd("foreshadow_suggest", true)
		h.logger.ForeshadowSuggestions(suggestions)
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) PostForeshadow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		PlantChapter  int    `json:"plant_chapter"`
		TargetChapter int    `json:"target_chapter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "缺少 name")
		return
	}
	if req.Description == "" {
		h.writeError(w, http.StatusBadRequest, "缺少 description")
		return
	}

	fs := Foreshadow{
		ID:            NextForeshadowID(h.state.Foreshadows),
		Name:          req.Name,
		Description:   req.Description,
		PlantChapter:  req.PlantChapter,
		TargetChapter: req.TargetChapter,
		Status:        ForeshadowPlanted,
		Events:        []ForeshadowEvent{},
	}

	h.state.Foreshadows = append(h.state.Foreshadows, fs)

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, fs)
}

func (h *Handlers) PutForeshadow(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	var id int
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的伏笔ID")
		return
	}

	var req struct {
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		PlantChapter  int             `json:"plant_chapter"`
		TargetChapter int             `json:"target_chapter"`
		Status        ForeshadowStatus `json:"status"`
		Resolution    string          `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	idx := -1
	for i, fs := range h.state.Foreshadows {
		if fs.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		h.writeError(w, http.StatusNotFound, "伏笔不存在")
		return
	}

	fs := &h.state.Foreshadows[idx]
	if req.Name != "" {
		fs.Name = req.Name
	}
	if req.Description != "" {
		fs.Description = req.Description
	}
	if req.PlantChapter > 0 {
		fs.PlantChapter = req.PlantChapter
	}
	if req.TargetChapter > 0 {
		fs.TargetChapter = req.TargetChapter
	}
	if req.Status != "" {
		fs.Status = req.Status
	}
	if req.Resolution != "" {
		fs.Resolution = req.Resolution
	}

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, fs)
}

func (h *Handlers) DeleteForeshadow(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	var id int
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的伏笔ID")
		return
	}

	idx := -1
	for i, fs := range h.state.Foreshadows {
		if fs.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		h.writeError(w, http.StatusNotFound, "伏笔不存在")
		return
	}

	h.state.Foreshadows = append(h.state.Foreshadows[:idx], h.state.Foreshadows[idx+1:]...)

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) PostForeshadowsConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Foreshadows []Foreshadow `json:"foreshadows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	for i := range req.Foreshadows {
		req.Foreshadows[i].ID = NextForeshadowID(h.state.Foreshadows) + i
		req.Foreshadows[i].Status = ForeshadowPlanted
		if req.Foreshadows[i].Events == nil {
			req.Foreshadows[i].Events = []ForeshadowEvent{}
		}
	}

	h.state.Foreshadows = append(h.state.Foreshadows, req.Foreshadows...)

	if err := SaveProgress(h.progressPath, h.state); err != nil {
		h.writeError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, h.state.Foreshadows)
}

func (h *Handlers) PostContinueImport(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "缺少 content 字段")
		return
	}

	go func() {
		h.logger.TaskStart("continue_analysis")

		h.logger.Info("正在分析已有内容...")
		analysis, err := AnalyzeExistingContent(h.apiCfg, h.cfg, body.Content)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("内容分析失败: %v", err))
			h.logger.TaskEnd("continue_analysis", false)
			return
		}

		h.pendingContinueContent = body.Content

		h.endTask()
		h.logger.Success(fmt.Sprintf("内容分析完成，发现 %d 章", len(analysis.Chapters)))
		h.logger.TaskEnd("continue_analysis", true)
		h.logger.ContinueAnalysisResult(analysis)
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) PostContinueConfirm(w http.ResponseWriter, r *http.Request) {
	if h.isTaskRunning() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	if h.state.Phase != "outline" {
		h.writeError(w, http.StatusBadRequest, "续写前请先重置进度")
		return
	}

	if h.pendingContinueContent == "" {
		h.writeError(w, http.StatusBadRequest, "请先分析内容")
		return
	}

	var analysis ContinueAnalysis
	if err := json.NewDecoder(r.Body).Decode(&analysis); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的JSON: "+err.Error())
		return
	}

	if len(analysis.Chapters) == 0 {
		h.writeError(w, http.StatusBadRequest, "分析结果中没有任何章节")
		return
	}

	content := h.pendingContinueContent
	h.pendingContinueContent = ""

	if err := ImportContinueAction(h.cfg, h.state, &analysis, content, h.progressPath, h.cfgPath); err != nil {
		h.writeError(w, http.StatusInternalServerError, "导入续写失败: "+err.Error())
		return
	}

	h.logger.Success("续写导入完成，已进入大纲阶段。")
	h.writeJSON(w, http.StatusOK, h.state)
}

func (h *Handlers) PostOutlineGenerateContinuation(w http.ResponseWriter, r *http.Request) {
	if !h.tryStartTask() {
		h.writeError(w, http.StatusConflict, "有任务正在运行，请等待完成")
		return
	}

	if h.state.Phase != "outline" {
		h.endTask()
		h.writeError(w, http.StatusBadRequest, "当前不在大纲阶段")
		return
	}

	var body struct {
		ChapterCount int `json:"chapter_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChapterCount <= 0 {
		body.ChapterCount = 5
	}

	go func() {
		h.logger.TaskStart("continuation_outline")

		h.logger.Info("正在生成续写大纲...")
		err := GenerateContinuationOutline(h.apiCfg, h.cfg, h.state, body.ChapterCount, h.progressPath, h.logger)

		if err != nil {
			h.endTask()
			h.logger.Error(fmt.Sprintf("续写大纲生成失败: %v", err))
			h.logger.TaskEnd("continuation_outline", false)
			return
		}

		h.endTask()
		h.logger.Success("续写大纲生成完成！")
		h.logger.TaskEnd("continuation_outline", true)
		h.broadcastProgress()
	}()

	h.writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handlers) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.logger.Subscribe()
	defer h.logger.Unsubscribe(ch)

	ctx := r.Context()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, err := w.Write(formatSSE(msg))
			if err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func writeFileAtomic(path string, data []byte) error {
	tmpPath := path + ".tmp"
	if err := writeFile(tmpPath, data); err != nil {
		return err
	}
	if err := renameFile(tmpPath, path); err != nil {
		deleteFile(tmpPath)
		return err
	}
	return nil
}

func writeFile(path string, data []byte) error {
	return writeFileImpl(path, data)
}

func deleteFile(path string) error {
	return deleteFileImpl(path)
}

func renameFile(old, new string) error {
	return renameFileImpl(old, new)
}
