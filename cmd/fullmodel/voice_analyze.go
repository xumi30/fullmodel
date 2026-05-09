package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xumi30/fullmodel/agent/brain"
	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
	"github.com/xumi30/fullmodel/processmessage"
	"github.com/xumi30/fullmodel/utils/logging"
)

const voiceTagAnalyzePrompt = `你是语音样本分析助手，请仅根据用户上传的这段音频（仅声音，无图像）进行分析输出。

【输出要求】
1) language：主要使用语言，仅限 zh、en、ja、ko、other 之一。
2) summary：用一小句中文概括该声音给人的整体听感与选声/社交人设参考（适合 TTS、声鉴、连麦场景）。
3) tags：打「短标签」数组，6～14 个为宜；每个标签不超过 8 个中文字（或对应英文单词），不要编号、不要前缀「类别：」。
   打标时请优先从下列「六类参考词库」中挑选最贴切的词；若词库无完全匹配项，可输出语义等价、简短的自创标签。
   尽量覆盖至少 2～3 个不同维度（例如人设 + 质感 + 情感，或人设 + 场景 + 趣味），避免标签全堆在同一维度。

【一、音色人设标签（核心，社交/声鉴向）】
按性别/年龄/气质：女声可参考：萝莉音、少女音、少萝音、御姐音、少御音、女王音、甜妹音、甜桃音、软糖音、御冷音、清冷音、气泡音、烟嗓。
男声可参考：正太音、少年音、青年音、青受音、青叔音、大叔音、低音炮、磁性音、公狗腰音。
中性/特色可参考：少年感、清朗音、薄荷音、奶音、糙汉音。

【二、听觉质感标签】
明亮系：清澈、透亮、干净、清脆、甜美、通透。
醇厚系：浑厚、低沉、磁性、温润、饱满、厚重。
特殊质感：沙哑、烟嗓、气泡、颗粒感、金属感、软糯、清脆（与明亮系重复词可只出现一次）。

【三、情感氛围标签】
治愈系：温柔、舒缓、安静、暖心、治愈、空灵、静谧。
情绪系：元气、活泼、甜酷、冷艳、慵懒、深情、伤感。
风格系：二次元、古风、御系、萌系、禁欲、清冷、魅惑。

【四、场景用途标签】
社交：连麦、哄睡、陪伴、电台、告白。
创作：配音、有声阅读、短视频解说、游戏语音（可细化为更短短语如「有声书」）。
功能：助眠、学习、通勤、健身、胎教、白噪音。

【五、声学与技术标签（听感可判断时再选）】
基础：音准、音域、节奏、响度、清晰度、流畅度。
进阶：自然度、韵律感、稳定度、共鸣、气息控制、音色辨识度。

【六、趣味/网络流行标签（贴切则选，勿硬套）】
人间理想音、男友音、女友音、声控福利、开口跪、神仙嗓音、宝藏声音、低音杀、甜系暴击、禁欲系 等。

只输出一段 JSON，不要 markdown 代码块，不要其它解释。格式严格为：
{"language":"zh","summary":"……","tags":["标签1","标签2","…"]}`

// handleVoiceAnalyzeTags serves POST /v1/voice/analyze-tags (multipart: field "audio" required).
// Uses KindOmni (Qwen-Omni compatible-mode) from config brains.omni; aggregates stream to JSON.
func handleVoiceAnalyzeTags(w http.ResponseWriter, r *http.Request, state *serverState) {
	start := time.Now()
	trace := traceForRequest(w, r)
	remote := r.RemoteAddr
	if r.Method != http.MethodPost {
		logging.Warn("[voice.analyze] method_not_allowed trace=%s remote=%s", trace, remote)
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	if state == nil || state.runner == nil {
		logging.Error("[voice.analyze] runner_nil trace=%s remote=%s", trace, remote)
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("runner unavailable"))
		return
	}
	logging.Info("[voice.analyze] begin trace=%s remote=%s client_trace_hdr=%q content_type=%q content_length=%d",
		trace, remote, r.Header.Get("X-Client-Trace-Id"), r.Header.Get("Content-Type"), r.ContentLength)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		logging.Warn("[voice.analyze] parse_multipart_failed trace=%s remote=%s err=%v", trace, remote, err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		logging.Warn("[voice.analyze] missing_audio trace=%s remote=%s err=%v", trace, remote, err)
		writeError(w, http.StatusBadRequest, fmt.Errorf("multipart: need audio file field: %w", err))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		logging.Warn("[voice.analyze] read_audio_failed trace=%s err=%v", trace, err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	filename := header.Filename
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	logging.Info("[voice.analyze] audio_ready trace=%s filename=%q bytes=%d mime=%q",
		trace, filename, len(data), mimeType)
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		prompt = voiceTagAnalyzePrompt
	}

	msg := processmessage.OmniMessage{
		Prompt: prompt,
		Audio:  brain.MediaResource{Data: data, MimeType: mimeType},
	}
	opts := processmessage.Options{
		Context:             r.Context(),
		Stream:              false,
		DisableDefaultTools: true,
	}

	runAt := time.Now()
	logging.Info("[voice.analyze] runner_run_begin trace=%s kind=omni", trace)
	result, err := state.runner.Run(r.Context(), agentruntime.Request{Message: msg, Options: opts})
	if err != nil {
		logging.Error("[voice.analyze] run_failed trace=%s remote=%s runner_elapsed=%s err=%v",
			trace, remote, time.Since(runAt).Truncate(time.Millisecond), err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if result == nil || result.Output == nil {
		logging.Error("[voice.analyze] empty_output trace=%s", trace)
		writeError(w, http.StatusBadGateway, fmt.Errorf("empty model output"))
		return
	}
	if !result.Output.Status.Success {
		logging.Warn("[voice.analyze] brain_status_fail trace=%s err=%q", trace, result.Output.Status.Error)
		writeError(w, http.StatusBadGateway, fmt.Errorf("%s", result.Output.Status.Error))
		return
	}

	var fullText string
	streamAt := time.Now()
	if result.Output.Stream != nil {
		logging.Info("[voice.analyze] stream_collect_begin trace=%s", trace)
		fullText, err = collectStreamText(result.Output.Stream)
		if err != nil {
			logging.Error("[voice.analyze] stream_collect_failed trace=%s stream_elapsed=%s err=%v",
				trace, time.Since(streamAt).Truncate(time.Millisecond), err)
			writeError(w, http.StatusBadGateway, err)
			return
		}
		logging.Info("[voice.analyze] stream_collect_ok trace=%s stream_elapsed=%s text_runes=%d text_preview=%q",
			trace, time.Since(streamAt).Truncate(time.Millisecond), len([]rune(fullText)),
			truncateForVoiceLog(fullText, 180))
	} else {
		fullText = strings.TrimSpace(result.Output.Content.Text)
		logging.Info("[voice.analyze] non_stream_text trace=%s text_runes=%d", trace, len([]rune(fullText)))
	}

	summary, tags, lang, parseOK := parseVoiceTagJSON(fullText)
	out := map[string]any{
		"source":           "omni",
		"language":         lang,
		"language_label":   languageLabel(lang),
		"summary":          summary,
		"tags":             tags,
		"parse_ok":         parseOK,
		"raw_model_output": fullText,
	}
	logging.Info("[voice.analyze] ok trace=%s parse_ok=%v lang=%q tags=%d total_elapsed=%s summary_preview=%q",
		trace, parseOK, lang, len(tags), time.Since(start).Truncate(time.Millisecond),
		truncateForVoiceLog(summary, 120))
	writeOK(w, out)
}

func truncateForVoiceLog(s string, maxRunes int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes]) + "…"
}

func collectStreamText(stream brain.StreamOutput) (string, error) {
	if stream == nil {
		return "", fmt.Errorf("nil stream")
	}
	var b strings.Builder
	for chunk := range stream.Text() {
		b.WriteString(chunk)
	}
	if err := stream.Wait(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func parseVoiceTagJSON(modelText string) (summary string, tags []string, lang string, parseOK bool) {
	obj := strings.TrimSpace(extractJSONObject(modelText))
	if obj == "" {
		return "", nil, "", false
	}
	var payload struct {
		Language string   `json:"language"`
		Summary  string   `json:"summary"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(obj), &payload); err != nil {
		return "", nil, "", false
	}
	lang = strings.TrimSpace(strings.ToLower(payload.Language))
	summary = strings.TrimSpace(payload.Summary)
	for _, t := range payload.Tags {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return summary, tags, lang, true
}

func languageLabel(code string) string {
	switch strings.TrimSpace(strings.ToLower(code)) {
	case "zh", "zh-cn", "cmn":
		return "中文"
	case "en":
		return "英文"
	case "ja":
		return "日语"
	case "ko":
		return "韩语"
	case "other", "":
		return "其它"
	default:
		return code
	}
}
