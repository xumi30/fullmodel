package fullmodel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/utils/logging"
)

const (
	QwenVoiceEnrollmentModel        = "qwen-voice-enrollment"
	QwenTTSVCRealtimeModel          = "qwen3-tts-vc-realtime-2026-01-15"
	QwenTTSFlashRealtimeModel       = "qwen3-tts-flash-realtime"
	QwenTTSInstructRealtimeModel    = "qwen3-tts-instruct-flash-realtime"
	QwenRealtimeModeServerCommit    = "server_commit"
	QwenRealtimeModeCommit          = "commit"
	RealtimeDialogRespondPrompt     = "prompt"
	RealtimeDialogRespondTranscript = "transcript"
)

// VoiceCloneRequest creates a custom Qwen voice from a short audio sample.
// TargetModel must match the later TTS model that will use the returned voice.
type VoiceCloneRequest struct {
	Audio         brain.MediaResource
	AudioDataURL  string
	TargetModel   string
	PreferredName string
	Language      string
	Text          string
	Model         string
}

type VoiceCloneResult struct {
	Voice        string         `json:"voice,omitempty"`
	TargetModel  string         `json:"target_model,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	Usage        map[string]any `json:"usage,omitempty"`
	AnalysisTags []string       `json:"analysis_tags,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

type VoiceListRequest struct {
	PageSize  int
	PageIndex int
}

type VoiceInfo struct {
	Voice       string         `json:"voice,omitempty"`
	TargetModel string         `json:"target_model,omitempty"`
	Status      string         `json:"status,omitempty"`
	Raw         map[string]any `json:"raw,omitempty"`
}

type VoiceListResult struct {
	Voices    []VoiceInfo    `json:"voices,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
}

// RealtimeTTSConfig configures Qwen-TTS Realtime WebSocket sessions.
type RealtimeTTSConfig struct {
	Model                string
	Voice                string
	Mode                 string
	LanguageType         string
	ResponseFormat       string
	SampleRate           int
	Instructions         string
	OptimizeInstructions bool
	Session              map[string]any
}

type RealtimeEvent struct {
	Type      string         `json:"type,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
	Audio     []byte         `json:"-"`
	ErrorCode string         `json:"error_code,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type RealtimeTTSSession struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	seq    int
	events chan RealtimeEvent
	audio  chan []byte
	errs   chan error
	done   chan struct{}
}

type RealtimeDialogConfig struct {
	WorkspaceID string
	AppID       string
	DialogID    string
	Upstream    map[string]any
	Downstream  map[string]any
	ClientInfo  map[string]any
	BizParams   map[string]any
	Model       string
}

type RealtimeDialogSession struct {
	conn     *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	taskID   string
	dialogID string
	mu       sync.Mutex
	events   chan map[string]any
	audio    chan []byte
	errs     chan error
	done     chan struct{}
}

func (c *Client) CloneVoice(ctx context.Context, req VoiceCloneRequest) (*VoiceCloneResult, error) {
	if strings.TrimSpace(req.AudioDataURL) == "" && strings.TrimSpace(req.Audio.URL) == "" && len(req.Audio.Data) == 0 {
		return nil, fmt.Errorf("voice clone requires audio data, audio data URL, or audio URL")
	}
	raw, err := c.doVoiceCustomization(ctx, voiceClonePayload(req))
	if err != nil {
		return nil, err
	}
	outMap, _ := raw["output"].(map[string]any)
	result := &VoiceCloneResult{
		Voice:        stringValue(outMap["voice"]),
		TargetModel:  stringValue(outMap["target_model"]),
		RequestID:    stringValue(raw["request_id"]),
		Usage:        mapValue(raw["usage"]),
		AnalysisTags: ParseVoiceCloneAnalysisTags(raw),
		Raw:          raw,
	}
	return result, nil
}

// ParseVoiceCloneAnalysisTags extracts optional descriptive tags from the voice
// customization JSON body (typically raw["output"]). The official DashScope
// enrollment API currently documents only voice and target_model in output;
// this helper is forward-compatible if the service or a gateway adds keys such
// as tags, labels, or analysis_tags. It does not call third-party products or fabricate tags.
func ParseVoiceCloneAnalysisTags(raw map[string]any) []string {
	if raw == nil {
		return nil
	}
	out := mapValue(raw["output"])
	if out == nil {
		return nil
	}
	keys := []string{
		"analysis_tags",
		"tags",
		"labels",
		"voice_tags",
		"quality_tags",
		"descriptions",
	}
	seen := map[string]struct{}{}
	var collected []string
	for _, k := range keys {
		for _, s := range stringListFromAny(out[k]) {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			collected = append(collected, s)
		}
	}
	return collected
}

func stringListFromAny(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		return []string{x}
	case []string:
		return x
	case []any:
		var out []string
		for _, it := range x {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (c *Client) ListVoices(ctx context.Context, req VoiceListRequest) (*VoiceListResult, error) {
	input := map[string]any{"action": "list"}
	if req.PageSize > 0 {
		input["page_size"] = req.PageSize
	}
	if req.PageIndex > 0 {
		input["page_index"] = req.PageIndex
	}
	raw, err := c.doVoiceCustomization(ctx, map[string]any{
		"model": QwenVoiceEnrollmentModel,
		"input": input,
	})
	if err != nil {
		return nil, err
	}
	result := &VoiceListResult{
		RequestID: stringValue(raw["request_id"]),
		Usage:     mapValue(raw["usage"]),
		Raw:       raw,
	}
	output := mapValue(raw["output"])
	items := anySlice(firstNonNil(output["voices"], output["voice_list"], output["data"]))
	for _, item := range items {
		infoMap := mapValue(item)
		result.Voices = append(result.Voices, VoiceInfo{
			Voice:       stringValue(firstNonNil(infoMap["voice"], infoMap["voice_id"], infoMap["name"])),
			TargetModel: stringValue(infoMap["target_model"]),
			Status:      stringValue(infoMap["status"]),
			Raw:         infoMap,
		})
	}
	return result, nil
}

func (c *Client) DeleteVoice(ctx context.Context, voice string) (string, error) {
	if strings.TrimSpace(voice) == "" {
		return "", fmt.Errorf("voice is required")
	}
	raw, err := c.doVoiceCustomization(ctx, map[string]any{
		"model": QwenVoiceEnrollmentModel,
		"input": map[string]any{
			"action": "delete",
			"voice":  strings.TrimSpace(voice),
		},
	})
	if err != nil {
		return "", err
	}
	return stringValue(raw["request_id"]), nil
}

func (c *Client) RealtimeTTS(ctx context.Context, cfg RealtimeTTSConfig) (*RealtimeTTSSession, error) {
	if c == nil || c.voiceConfig == nil {
		return nil, fmt.Errorf("fullmodel client is nil")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = strings.TrimSpace(c.voiceConfig.Model)
	}
	// This WebSocket is Qwen realtime TTS only; voice brain may use non-realtime ids (e.g. cosyvoice).
	if model == "" || !strings.Contains(strings.ToLower(model), "realtime") {
		model = QwenTTSFlashRealtimeModel
	}
	logging.Info("[voice.realtime_tts] phase=begin effective_model=%s voice=%s mode=%s format=%s sample_rate=%d region=%s",
		model, defaultString(cfg.Voice, "Cherry"), defaultString(cfg.Mode, QwenRealtimeModeServerCommit),
		defaultString(cfg.ResponseFormat, "pcm"), defaultInt(cfg.SampleRate, 24000), c.voiceConfig.Region)

	conn, err := c.dialVoiceWS(ctx, "tts_realtime_ws", "wss://dashscope.aliyuncs.com/api-ws/v1/realtime", "wss://dashscope-intl.aliyuncs.com/api-ws/v1/realtime", model)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &RealtimeTTSSession{
		conn:   conn,
		ctx:    sessionCtx,
		cancel: cancel,
		events: make(chan RealtimeEvent, 64),
		audio:  make(chan []byte, 64),
		errs:   make(chan error, 1),
		done:   make(chan struct{}),
	}
	go s.readLoop()

	update := map[string]any{
		"model":           model,
		"voice":           defaultString(cfg.Voice, "Cherry"),
		"mode":            defaultString(cfg.Mode, QwenRealtimeModeServerCommit),
		"language_type":   defaultString(cfg.LanguageType, "Chinese"),
		"response_format": defaultString(cfg.ResponseFormat, "pcm"),
		"sample_rate":     defaultInt(cfg.SampleRate, 24000),
	}
	if cfg.Instructions != "" {
		update["instructions"] = cfg.Instructions
		update["optimize_instructions"] = cfg.OptimizeInstructions
	}
	for k, v := range cfg.Session {
		update[k] = v
	}
	if err := s.send("session.update", map[string]any{"session": update}); err != nil {
		_ = s.Close()
		return nil, err
	}
	logging.Info("[voice.realtime_tts] phase=session_update_sent model=%s voice=%s mode=%s sample_rate=%v format=%s lang=%s",
		model, update["voice"], update["mode"], update["sample_rate"], update["response_format"], update["language_type"])
	return s, nil
}

func (c *Client) RealtimeTTSBytes(ctx context.Context, text string, cfg RealtimeTTSConfig) ([]byte, error) {
	session, err := c.RealtimeTTS(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if err := session.AppendText(text); err != nil {
		return nil, err
	}
	if cfg.Mode == QwenRealtimeModeCommit {
		if err := session.Commit(); err != nil {
			return nil, err
		}
	}
	if err := session.Finish(); err != nil {
		return nil, err
	}
	return session.CollectAudio(ctx)
}

func (s *RealtimeTTSSession) AppendText(text string) error {
	return s.send("input_text_buffer.append", map[string]any{"text": text})
}

func (s *RealtimeTTSSession) Commit() error {
	return s.send("input_text_buffer.commit", nil)
}

func (s *RealtimeTTSSession) Clear() error {
	return s.send("input_text_buffer.clear", nil)
}

func (s *RealtimeTTSSession) Finish() error {
	return s.send("session.finish", nil)
}

func (s *RealtimeTTSSession) Events() <-chan RealtimeEvent { return s.events }

func (s *RealtimeTTSSession) Audio() <-chan []byte { return s.audio }

func (s *RealtimeTTSSession) Close() error {
	logging.Info("[voice.realtime_tts] phase=session_close")
	s.cancel()
	err := s.conn.Close()
	if err != nil {
		logging.Warn("[voice.realtime_tts] session_close_ws err=%v", err)
	}
	return err
}

func (s *RealtimeTTSSession) CollectAudio(ctx context.Context) ([]byte, error) {
	var out bytes.Buffer
	for {
		select {
		case <-ctx.Done():
			return out.Bytes(), ctx.Err()
		case err := <-s.errs:
			if err != nil {
				return out.Bytes(), err
			}
		case chunk, ok := <-s.audio:
			if ok {
				out.Write(chunk)
			}
		case <-s.done:
			return out.Bytes(), nil
		}
	}
}

func (s *RealtimeTTSSession) send(eventType string, body map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	msg := map[string]any{
		"event_id": fmt.Sprintf("event_%d", s.seq),
		"type":     eventType,
	}
	for k, v := range body {
		msg[k] = v
	}
	logging.Info("[voice.realtime_tts] client_send type=%s seq=%d event_id=%s", eventType, s.seq, msg["event_id"])
	return s.conn.WriteJSON(msg)
}

func (s *RealtimeTTSSession) readLoop() {
	var exitReason string
	var audioDeltas int64
	defer func() {
		logging.Info("[voice.realtime_tts] phase=readLoop_exit reason=%s upstream_audio_deltas=%d", exitReason, atomic.LoadInt64(&audioDeltas))
	}()
	defer close(s.done)
	defer close(s.events)
	defer close(s.audio)
	logging.Info("[voice.realtime_tts] phase=readLoop_start")
	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			exitReason = "ws_read_error"
			logging.Warn("[voice.realtime_tts] readLoop read_error err=%v", err)
			select {
			case <-s.ctx.Done():
			case s.errs <- err:
			default:
			}
			return
		}
		if msgType == websocket.BinaryMessage {
			logging.Warn("[voice.realtime_tts] readLoop unexpected_binary bytes=%d", len(data))
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			logging.Warn("[voice.realtime_tts] readLoop skip_non_json bytes=%d err=%v preview=%s",
				len(data), err, voiceLogPreview(string(data), 160))
			continue
		}
		ev := RealtimeEvent{Type: stringValue(raw["type"]), EventID: stringValue(raw["event_id"]), Raw: raw}
		if ev.Type != "" && ev.Type != "response.audio.delta" {
			logging.Info("[voice.realtime_tts] upstream_event type=%s event_id=%s", ev.Type, ev.EventID)
		}
		if errMap := mapValue(raw["error"]); len(errMap) > 0 {
			ev.ErrorCode = stringValue(errMap["code"])
			ev.Error = stringValue(errMap["message"])
			exitReason = "upstream_error_event"
			logging.Error("[voice.realtime_tts] upstream_error code=%s message=%s", ev.ErrorCode, ev.Error)
			select {
			case s.events <- ev:
			default:
			}
			select {
			case s.errs <- fmt.Errorf("%s: %s", ev.ErrorCode, ev.Error):
			default:
			}
			return
		}
		if ev.Type == "response.audio.delta" {
			deltaB64 := stringValue(raw["delta"])
			chunk, err := base64.StdEncoding.DecodeString(deltaB64)
			if err != nil {
				logging.Warn("[voice.realtime_tts] audio_delta_b64_decode_failed err=%v delta_len=%d preview=%s",
					err, len(deltaB64), voiceLogPreview(deltaB64, 48))
			} else if len(chunk) == 0 {
				logging.Warn("[voice.realtime_tts] audio_delta_empty_after_decode delta_len=%d", len(deltaB64))
			} else {
				n := atomic.AddInt64(&audioDeltas, 1)
				if n == 1 {
					logging.Info("[voice.realtime_tts] phase=first_upstream_audio_delta bytes=%d", len(chunk))
				} else if n%100 == 0 {
					logging.Info("[voice.realtime_tts] upstream_audio_delta_progress deltas=%d last_chunk_bytes=%d", n, len(chunk))
				}
				ev.Audio = chunk
				select {
				case s.audio <- chunk:
				case <-s.ctx.Done():
					exitReason = "ctx_done_while_sending_audio"
					return
				}
			}
		}
		select {
		case s.events <- ev:
		case <-s.ctx.Done():
			exitReason = "ctx_done_while_dispatch"
			return
		}
		if ev.Type == "session.finished" {
			exitReason = "session.finished"
			logging.Info("[voice.realtime_tts] phase=upstream_session_finished_event")
			return
		}
	}
}

func (c *Client) RealtimeDialog(ctx context.Context, cfg RealtimeDialogConfig) (*RealtimeDialogSession, error) {
	if c == nil || c.voiceConfig == nil {
		return nil, fmt.Errorf("fullmodel client is nil")
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" || strings.TrimSpace(cfg.AppID) == "" {
		return nil, fmt.Errorf("workspace id and app id are required")
	}
	conn, err := c.dialVoiceWS(ctx, "realtime_dialog_ws", "wss://dashscope.aliyuncs.com/api-ws/v1/inference", "wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference", "")
	if err != nil {
		return nil, err
	}
	taskID, err := brainRandomHex32()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &RealtimeDialogSession{
		conn:     conn,
		ctx:      sessionCtx,
		cancel:   cancel,
		taskID:   taskID,
		dialogID: strings.TrimSpace(cfg.DialogID),
		events:   make(chan map[string]any, 64),
		audio:    make(chan []byte, 64),
		errs:     make(chan error, 1),
		done:     make(chan struct{}),
	}
	go s.readLoop()
	payload := map[string]any{
		"task_group": "aigc",
		"task":       "multimodal-generation",
		"function":   "generation",
		"model":      defaultString(cfg.Model, "multimodal-dialog"),
		"input": map[string]any{
			"directive":    "Start",
			"workspace_id": strings.TrimSpace(cfg.WorkspaceID),
			"app_id":       strings.TrimSpace(cfg.AppID),
		},
		"parameters": map[string]any{
			"upstream":    defaultMap(cfg.Upstream, map[string]any{"type": "AudioOnly", "mode": "push2talk", "audio_format": "pcm", "sample_rate": 16000}),
			"downstream":  defaultMap(cfg.Downstream, map[string]any{"audio_format": "pcm", "sample_rate": 24000}),
			"client_info": defaultMap(cfg.ClientInfo, map[string]any{"user_agent": "fullmodel-go-sdk"}),
		},
	}
	if s.dialogID != "" {
		payload["input"].(map[string]any)["dialog_id"] = s.dialogID
	}
	if len(cfg.BizParams) > 0 {
		payload["parameters"].(map[string]any)["biz_params"] = cfg.BizParams
	}
	if err := s.write("run-task", payload); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *RealtimeDialogSession) Events() <-chan map[string]any { return s.events }

func (s *RealtimeDialogSession) Audio() <-chan []byte { return s.audio }

func (s *RealtimeDialogSession) Errors() <-chan error { return s.errs }

func (s *RealtimeDialogSession) SendAudio(chunk []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, chunk)
}

func (s *RealtimeDialogSession) SendSpeech() error {
	return s.directive("SendSpeech", nil)
}

func (s *RealtimeDialogSession) StopSpeech() error {
	return s.directive("StopSpeech", nil)
}

func (s *RealtimeDialogSession) CancelSpeech() error {
	return s.directive("CancelSpeech", nil)
}

func (s *RealtimeDialogSession) RequestToSpeak() error {
	return s.directive("RequestToSpeak", nil)
}

func (s *RealtimeDialogSession) LocalRespondingStarted() error {
	return s.directive("LocalRespondingStarted", nil)
}

func (s *RealtimeDialogSession) LocalRespondingEnded() error {
	return s.directive("LocalRespondingEnded", nil)
}

func (s *RealtimeDialogSession) RequestToRespond(kind, text string, parameters map[string]any) error {
	input := map[string]any{
		"directive": "RequestToRespond",
		"type":      kind,
		"text":      text,
	}
	if s.dialogID != "" {
		input["dialog_id"] = s.dialogID
	}
	payload := map[string]any{"input": input}
	if len(parameters) > 0 {
		payload["parameters"] = parameters
	}
	return s.write("continue-task", payload)
}

func (s *RealtimeDialogSession) Finish() error {
	return s.write("finish-task", map[string]any{"input": map[string]any{}})
}

func (s *RealtimeDialogSession) Close() error {
	s.cancel()
	return s.conn.Close()
}

func (s *RealtimeDialogSession) directive(name string, extra map[string]any) error {
	input := map[string]any{"directive": name}
	if s.dialogID != "" {
		input["dialog_id"] = s.dialogID
	}
	for k, v := range extra {
		input[k] = v
	}
	return s.write("continue-task", map[string]any{"input": input})
}

func (s *RealtimeDialogSession) write(action string, payload map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(map[string]any{
		"header": map[string]any{
			"action":    action,
			"task_id":   s.taskID,
			"streaming": "duplex",
		},
		"payload": payload,
	})
}

func (s *RealtimeDialogSession) readLoop() {
	defer close(s.done)
	defer close(s.events)
	defer close(s.audio)
	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case <-s.ctx.Done():
			case s.errs <- err:
			default:
			}
			return
		}
		if msgType == websocket.BinaryMessage {
			cp := append([]byte(nil), data...)
			select {
			case s.audio <- cp:
			case <-s.ctx.Done():
				return
			}
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		if output := mapValue(mapValue(raw["payload"])["output"]); len(output) > 0 {
			if dialogID := stringValue(output["dialog_id"]); dialogID != "" {
				s.dialogID = dialogID
			}
		}
		select {
		case s.events <- raw:
		case <-s.ctx.Done():
			return
		}
	}
}

func (c *Client) doVoiceCustomization(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if c == nil || c.voiceConfig == nil {
		return nil, fmt.Errorf("fullmodel client is nil")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.voiceCustomizationURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.voiceConfig.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("voice customization returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decoded, fmt.Errorf("voice customization returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return decoded, nil
}

func (c *Client) voiceCustomizationURL() string {
	if endpoint, ok := c.voiceConfig.APIEndpoints["voice_customization"]; ok && strings.TrimSpace(endpoint) != "" {
		return endpoint
	}
	if c.voiceConfig.Region == brain.RegionSingapore {
		return "https://dashscope-intl.aliyuncs.com/api/v1/services/audio/tts/customization"
	}
	return "https://dashscope.aliyuncs.com/api/v1/services/audio/tts/customization"
}

// voiceRealtimeWSURL ensures ?model= is present on the Qwen realtime TTS WebSocket URL (required by DashScope).
func voiceRealtimeWSURL(endpoint, model string) string {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" || model == "" {
		return endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint + "?model=" + url.QueryEscape(model)
	}
	q := u.Query()
	if q.Get("model") == "" {
		q.Set("model", model)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) dialVoiceWS(ctx context.Context, endpointKey, cnURL, intlURL, attachModel string) (*websocket.Conn, error) {
	endpoint := cnURL
	if c.voiceConfig.Region == brain.RegionSingapore {
		endpoint = intlURL
	}
	if custom, ok := c.voiceConfig.APIEndpoints[endpointKey]; ok && strings.TrimSpace(custom) != "" {
		endpoint = strings.TrimSpace(custom)
	}
	if strings.TrimSpace(attachModel) != "" {
		endpoint = voiceRealtimeWSURL(endpoint, attachModel)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.voiceConfig.APIKey)
	header.Set("User-Agent", "fullmodel-go-sdk")
	logging.Info("[voice.ws] dial endpoint_key=%s endpoint=%s", endpointKey, endpoint)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if resp != nil {
			logging.Error("[voice.ws] dial failed endpoint_key=%s err=%v http_status=%s", endpointKey, err, resp.Status)
		} else {
			logging.Error("[voice.ws] dial failed endpoint_key=%s err=%v", endpointKey, err)
		}
		return nil, err
	}
	logging.Info("[voice.ws] phase=dial_ok endpoint_key=%s proto=%s", endpointKey, conn.Subprotocol())
	return conn, err
}

func voiceClonePayload(req VoiceCloneRequest) map[string]any {
	model := defaultString(req.Model, QwenVoiceEnrollmentModel)
	input := map[string]any{
		"action":         "create",
		"target_model":   defaultString(req.TargetModel, QwenTTSVCRealtimeModel),
		"preferred_name": strings.TrimSpace(req.PreferredName),
		"audio":          map[string]any{"data": voiceAudioData(req)},
	}
	if req.Text != "" {
		input["text"] = req.Text
	}
	if req.Language != "" {
		input["language"] = req.Language
	}
	return map[string]any{"model": model, "input": input}
}

func voiceAudioData(req VoiceCloneRequest) string {
	if strings.TrimSpace(req.AudioDataURL) != "" {
		return strings.TrimSpace(req.AudioDataURL)
	}
	if strings.TrimSpace(req.Audio.URL) != "" {
		return strings.TrimSpace(req.Audio.URL)
	}
	mimeType := strings.TrimSpace(req.Audio.MimeType)
	if mimeType == "" {
		mimeType = http.DetectContentType(req.Audio.Data)
		if exts, _ := mime.ExtensionsByType(mimeType); len(exts) == 0 {
			mimeType = "audio/mpeg"
		}
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(req.Audio.Data)
}

func voiceLogPreview(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes]) + "…"
}

func brainRandomHex32() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b[:]), nil
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func mapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func anySlice(v any) []any {
	if items, ok := v.([]any); ok {
		return items
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultMap(value, fallback map[string]any) map[string]any {
	if len(value) > 0 {
		return value
	}
	return fallback
}
