package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/agent/runtime"
	"github.com/xumi30/fullmodel/utils/logging"
)

const maxVoiceDialogTurnAudio = 20 << 20 // 20 MiB per utterance

// STT / LLM / TTS 在 default.log 中单字段最大可读长度（仍会加 … 后缀）
const dialogLogSpanTextMaxRunes = 8192

func dialogTruncateForLog(s string) string {
	return logging.TruncateRunes(strings.TrimSpace(s), dialogLogSpanTextMaxRunes)
}
// dialogConnState carries per-WebSocket session + pending mic audio + TTS parameters.
type dialogConnState struct {
	sessionID        string
	systemRemembered bool
	tts              fullmodel.RealtimeTTSConfig
	audioMIME        string
	turnSeq          int
	audioTurn        []byte
	audioChunks      int // binary frames appended for current utterance (for log throttle)
}

type dialogInbound struct {
	Op        string `json:"op"`
	SessionID string `json:"session_id"`
	Session   string `json:"session"`
	System    string `json:"system"`
	AudioMIME string `json:"audio_mime"`

	Voice        string `json:"voice"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	LanguageType string `json:"language_type"`
	Format       string `json:"format"`
	SampleRate   int    `json:"sample_rate"`
	Instructions string `json:"instructions"`

	OptimizeInstructions json.RawMessage `json:"optimize_instructions"`
}

func dialogTTSConfigFromQuery(q url.Values, voiceRealtimeWSModel string) fullmodel.RealtimeTTSConfig {
	model := strings.TrimSpace(voiceRealtimeWSModel)
	if model == "" {
		model = strings.TrimSpace(q.Get("model"))
	}
	return fullmodel.RealtimeTTSConfig{
		Voice:                firstNonEmpty(q.Get("voice"), "Cherry"),
		Model:                model,
		Mode:                 firstNonEmpty(q.Get("mode"), fullmodel.QwenRealtimeModeServerCommit),
		LanguageType:         firstNonEmpty(q.Get("language_type"), "Chinese"),
		ResponseFormat:       firstNonEmpty(q.Get("format"), "pcm"),
		SampleRate:           queryPositiveInt(q, "sample_rate", 24000),
		Instructions:         strings.TrimSpace(q.Get("instructions")),
		OptimizeInstructions: strings.EqualFold(q.Get("optimize_instructions"), "true") || q.Get("optimize_instructions") == "1",
	}
}

// handleVoiceDialogStream is one WebSocket: binary audio utterance → ASR → streaming LLM → realtime TTS.
//
// GET /v1/voice/dialog/stream  (same query knobs as /v1/voice/tts/stream plus optional audio_mime for ASR bytes)
//
// Client → Server:
//
//   - {"op":"ping"}
//
//   - {"op":"config", …} optional: session_id, system, voice, model, …, audio_mime
//
//   - binary frames (mic audio chunks, concatenated WAV by default)
//
//   - {"op":"end_turn"} after audio for that utterance
//
// Server → Client:
//
//	per turn (latency-first synthesis): JSON {"op":"asr"} → PCM binaries + intermediate {"op":"event"} matching /tts/stream → JSON {"op":"done"} when TTS completes → JSON {"op":"assistant"} full reply transcript.
//
//	LLM streams into TTS in phrase-sized segments (strong punctuation, comma after min length, or max ~56 runes) so synthesis starts quickly.
func handleVoiceDialogStream(w http.ResponseWriter, r *http.Request, sdk *fullmodel.Client, modelFromVoiceRealtimeWSConfig string) {
	start := time.Now()
	if sdk == nil {
		logging.Error("[voice.dialog] phase=reject reason=sdk_nil")
		http.Error(w, "voice dialog client unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := ttsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Warn("[voice.dialog] phase=ws_upgrade_failed client_remote=%s err=%v", r.RemoteAddr, err)
		return
	}
	remote := conn.RemoteAddr().String()
	logging.Info("[voice.dialog] phase=ws_upgrade_ok client_remote=%s path=%s query_masked=%s", remote, r.URL.Path, dialogMaskRawQuery(r.URL.RawQuery))
	defer func() {
		logging.Info("[voice.dialog] phase=handler_done client_remote=%s elapsed=%s",
			remote, time.Since(start).Truncate(time.Millisecond))
		_ = conn.Close()
	}()

	ww := &wsConnWriter{conn: conn}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	st := &dialogConnState{
		tts:       dialogTTSConfigFromQuery(r.URL.Query(), modelFromVoiceRealtimeWSConfig),
		audioMIME: firstNonEmpty(r.URL.Query().Get("audio_mime"), "audio/wav"),
	}

	for {
		if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			return
		}
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				logging.Warn("[voice.dialog] read_err client_remote=%s err=%v", remote, err)
			}
			return
		}
		if mt == websocket.BinaryMessage {
			if err := st.ingestAudio(remote, data); err != nil {
				logging.Warn("[voice.dialog] ingest_audio_err client_remote=%s err=%v", remote, err)
				_ = ww.writeJSON(map[string]any{"op": "error", "message": err.Error()})
			}
			continue
		}
		var msg dialogInbound
		if err := json.Unmarshal(data, &msg); err != nil {
			logging.Warn("[voice.dialog] json_invalid client_remote=%s err=%v preview=%s",
				remote, err, previewRunes(string(data), 200))
			_ = ww.writeJSON(map[string]any{"op": "error", "message": fmt.Sprintf("invalid json: %v", err)})
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Op)) {
		case "ping":
			logging.Info("[voice.dialog] client_op_ping client_remote=%s", remote)
			_ = ww.writeJSON(map[string]any{"op": "pong", "t": time.Now().UnixMilli()})
		case "config":
			st.applyConfig(msg, sdk, remote)
			_ = ww.writeJSON(map[string]any{"op": "config_ok", "session_id": st.sessionID})
		case "end_turn":
			logging.Info("[voice.dialog] client_op_end_turn client_remote=%s buffered_bytes=%d audio_chunks=%d",
				remote, len(st.audioTurn), st.audioChunks)
			t0 := time.Now()
			if err := st.flushTurn(ctx, ww, sdk, remote); err != nil {
				logging.Error("[voice.dialog] turn_failed client_remote=%s elapsed=%s err=%v",
					remote, time.Since(t0).Truncate(time.Millisecond), err)
				_ = ww.writeJSON(map[string]any{"op": "error", "message": err.Error()})
			} else {
				logging.Info("[voice.dialog] turn_success client_remote=%s elapsed_total=%s",
					remote, time.Since(t0).Truncate(time.Millisecond))
			}
		default:
			logging.Warn("[voice.dialog] client_op_unknown client_remote=%s raw_op=%q", remote, msg.Op)
			_ = ww.writeJSON(map[string]any{"op": "error", "message": fmt.Sprintf("unknown op %q", msg.Op)})
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func parseOptimizeInstructionFlag(raw json.RawMessage) (*bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return &b, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "1", "true", "yes", "on":
		v := true
		return &v, nil
	case "0", "false", "no", "off":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("optimize_instructions expects bool or truthy string")
	}
}

func (st *dialogConnState) ensureSessionID() error {
	if strings.TrimSpace(st.sessionID) != "" {
		return nil
	}
	id, err := runtime.RandomPublicID("dlg")
	if err != nil {
		st.sessionID = fmt.Sprintf("dlg-%d", time.Now().UnixNano())
		return nil
	}
	st.sessionID = id
	return nil
}

func (st *dialogConnState) applyConfig(msg dialogInbound, sdk *fullmodel.Client, remote string) {
	if tid := firstNonEmpty(strings.TrimSpace(msg.SessionID), strings.TrimSpace(msg.Session)); tid != "" {
		st.sessionID = tid
	} else if st.sessionID == "" {
		_ = st.ensureSessionID()
	}
	if sys := strings.TrimSpace(msg.System); sys != "" && !st.systemRemembered {
		_ = st.ensureSessionID()
		sdk.Memory().RememberSystem(st.sessionID, sys)
		st.systemRemembered = true
		logging.Info("[voice.dialog.client] config system remembered client_remote=%s session=%q runes=%d",
			remote, st.sessionID, len([]rune(sys)))
	}
	if v := strings.TrimSpace(msg.Voice); v != "" {
		st.tts.Voice = v
	}
	if m := strings.TrimSpace(msg.Model); m != "" {
		st.tts.Model = m
	}
	if m := strings.TrimSpace(msg.Mode); m != "" {
		st.tts.Mode = m
	}
	if m := strings.TrimSpace(msg.LanguageType); m != "" {
		st.tts.LanguageType = m
	}
	if m := strings.TrimSpace(msg.Format); m != "" {
		st.tts.ResponseFormat = m
	}
	if msg.SampleRate > 0 {
		st.tts.SampleRate = msg.SampleRate
	}
	if ins := strings.TrimSpace(msg.Instructions); ins != "" {
		st.tts.Instructions = ins
	}
	if b, err := parseOptimizeInstructionFlag(msg.OptimizeInstructions); err == nil && b != nil {
		st.tts.OptimizeInstructions = *b
	}
	if mime := strings.TrimSpace(msg.AudioMIME); mime != "" {
		st.audioMIME = mime
		logging.Info("[voice.dialog] config_audio_mime client_remote=%s mime=%q", remote, mime)
	}
	logging.Info("[voice.dialog] config_applied client_remote=%s session=%q voice=%q model=%q mode=%s tts_sr=%d lang=%q mime=%q opt_instr=%v",
		remote, st.sessionID, st.tts.Voice, st.tts.Model, st.tts.Mode, st.tts.SampleRate, st.tts.LanguageType,
		st.audioMIME, st.tts.OptimizeInstructions)
}

func (st *dialogConnState) ingestAudio(remote string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	if len(st.audioTurn)+len(chunk) > maxVoiceDialogTurnAudio {
		return fmt.Errorf("audio exceeds %d bytes per turn", maxVoiceDialogTurnAudio)
	}
	st.audioTurn = append(st.audioTurn, chunk...)
	st.audioChunks++
	buf := len(st.audioTurn)
	prev := buf - len(chunk)
	if st.audioChunks == 1 || st.audioChunks%25 == 0 || (prev > 0 && prev/(256*1024) != buf/(256*1024)) {
		logging.Info("[voice.dialog] audio_buffered client_remote=%s chunk #%d chunk_bytes=%d total_bytes=%d",
			remote, st.audioChunks, len(chunk), buf)
	}
	return nil
}

func (st *dialogConnState) takeAudioBuf() []byte {
	out := st.audioTurn
	st.audioTurn = nil
	st.audioChunks = 0
	return out
}

func (st *dialogConnState) flushTurn(ctx context.Context, ww *wsConnWriter, sdk *fullmodel.Client, remote string) error {
	audio := st.takeAudioBuf()
	if len(audio) == 0 {
		return fmt.Errorf("no audio buffered; send binary chunks before end_turn")
	}
	_ = st.ensureSessionID()
	st.turnSeq++
	seq := st.turnSeq
	tAll := time.Now()

	logging.Info("[voice.dialog] turn_begin chain=1/7 client_remote=%s turn_seq=%d session=%q wav_bytes=%d audio_mime=%q",
		remote, seq, st.sessionID, len(audio), st.audioMIME)

	tASR := time.Now()
	userText, err := sdk.ASR(ctx, brain.MediaResource{Data: audio, MimeType: st.audioMIME})
	if err != nil {
		logging.Error("[voice.dialog] asr_failed chain=2/7 client_remote=%s turn_seq=%d elapsed=%s err=%v",
			remote, seq, time.Since(tASR).Truncate(time.Millisecond), err)
		return fmt.Errorf("asr: %w", err)
	}
	userText = strings.TrimSpace(userText)
	logging.Info("[voice.dialog] phase=stt_done chain=2/7 client_remote=%s turn_seq=%d elapsed=%s transcript_runes=%d transcript=%s",
		remote, seq, time.Since(tASR).Truncate(time.Millisecond), len([]rune(userText)), dialogTruncateForLog(userText))
	if err := ww.writeJSON(map[string]any{"op": "asr", "text": userText, "turn_seq": seq}); err != nil {
		return err
	}
	if userText == "" {
		logging.Warn("[voice.dialog] asr_empty chain=stop client_remote=%s turn_seq=%d", remote, seq)
		return fmt.Errorf("empty transcript")
	}

	logging.Info("[voice.dialog] llm_stream_start chain=3/7 client_remote=%s turn_seq=%d session=%q tools_disabled=true stream=StreamChat",
		remote, seq, st.sessionID)
	logging.Info("[voice.dialog] phase=llm_user_input chain=3/7 client_remote=%s turn_seq=%d session=%q user_text_runes=%d user_text=%s",
		remote, seq, st.sessionID, len([]rune(userText)), dialogTruncateForLog(userText))
	tLLM := time.Now()
	stream, err := sdk.StreamChat(ctx, st.sessionID, userText, fullmodel.WithRuntimeTools(false))
	if err != nil {
		logging.Error("[voice.dialog] llm_stream_dial_failed client_remote=%s turn_seq=%d err=%v", remote, seq, err)
		return fmt.Errorf("chat stream: %w", err)
	}
	if stream == nil {
		return fmt.Errorf("chat stream unavailable")
	}

	logging.Info("[voice.dialog] tts_upstream_start chain=4/7 client_remote=%s turn_seq=%d voice=%q model=%q mode=%s flow=\"LLM deltas → peelTTSChunks → AppendText → PCM via pumpRealtimeTTS\"",
		remote, seq, st.tts.Voice, st.tts.Model, st.tts.Mode)
	tTTS := time.Now()
	ttsCtx, cancelTTS := context.WithCancel(ctx)
	defer cancelTTS()
	ttsSession, err := sdk.RealtimeTTS(ttsCtx, st.tts)
	if err != nil {
		stream.Cancel()
		_ = stream.Wait()
		logging.Error("[voice.dialog] realtime_tts_failed chain=4/7 client_remote=%s turn_seq=%d elapsed=%s err=%v",
			remote, seq, time.Since(tTTS).Truncate(time.Millisecond), err)
		return fmt.Errorf("realtime tts: %w", err)
	}
	logging.Info("[voice.dialog] realtime_tts_ready chain=4/7 client_remote=%s turn_seq=%d handshake_elapsed=%s",
		remote, seq, time.Since(tTTS).Truncate(time.Millisecond))
	defer func() { _ = ttsSession.Close() }()

	pumpDone := make(chan struct{})
	go pumpRealtimeTTS(ttsCtx, ttsSession, ww, pumpDone, remote, seq)

	var assembled strings.Builder
	pending := ""
	var streamErr error
	llmChunks := 0
	ttsAppends := 0
	var ttsSegSample []string
loop:
	for {
		select {
		case <-ctx.Done():
			streamErr = ctx.Err()
			stream.Cancel()
			logging.Warn("[voice.dialog] llm_ctx_done client_remote=%s turn_seq=%d err=%v", remote, seq, ctx.Err())
			break loop
		case err := <-stream.Error():
			if err != nil {
				streamErr = fmt.Errorf("llm stream: %w", err)
				stream.Cancel()
				logging.Error("[voice.dialog] llm_stream_err client_remote=%s turn_seq=%d err=%v", remote, seq, err)
				break loop
			}
		case chunk, ok := <-stream.Text():
			if !ok {
				logging.Info("[voice.dialog] phase=llm_stream_tokens_done chain=5/7 client_remote=%s turn_seq=%d upstream_chunks=%d llm_elapsed=%s assembled_runes=%d assistant_reply=%s",
					remote, seq, llmChunks, time.Since(tLLM).Truncate(time.Millisecond),
					len([]rune(assembled.String())), dialogTruncateForLog(assembled.String()))
				break loop
			}
			llmChunks++
			cr := len([]rune(chunk))
			if llmChunks == 1 || cr >= 240 || llmChunks%14 == 0 {
				logging.Info("[voice.dialog] phase=llm_stream_delta chain=5/7 client_remote=%s turn_seq=%d delta_idx=%d delta_runes=%d llm_ttft_ms=%s delta_tail=%s",
					remote, seq, llmChunks, cr, time.Since(tLLM).Truncate(time.Millisecond), dialogTruncateForLog(chunk))
			}
			assembled.WriteString(chunk)
			pending += chunk
			for _, seg := range peelTTSChunks(&pending, false) {
				if seg == "" {
					continue
				}
				ttsAppends++
				a0 := time.Now()
				if err := ttsSession.AppendText(seg); err != nil && streamErr == nil {
					streamErr = fmt.Errorf("tts append: %w", err)
					stream.Cancel()
					logging.Error("[voice.dialog] tts_append_failed client_remote=%s turn_seq=%d append_idx=%d err=%v",
						remote, seq, ttsAppends, err)
					break loop
				}
				if ttsAppends <= 12 || ttsAppends%10 == 0 {
					logging.Info("[voice.dialog] phase=tts_append_text chain=6/7 client_remote=%s turn_seq=%d append_idx=%d seg_chars=%d seg_runes=%d took=%s seg=%s",
						remote, seq, ttsAppends, len(seg), len([]rune(seg)), time.Since(a0).Truncate(time.Millisecond), dialogTruncateForLog(seg))
				}
				if len(ttsSegSample) < 10 {
					ttsSegSample = append(ttsSegSample, seg)
				}
			}
		}
	}

	for _, seg := range peelTTSChunks(&pending, true) {
		if seg == "" {
			continue
		}
		ttsAppends++
		apErr := ttsSession.AppendText(seg)
		if apErr != nil && streamErr == nil {
			streamErr = fmt.Errorf("tts append: %w", apErr)
			logging.Error("[voice.dialog] tts_append_eof_failed client_remote=%s turn_seq=%d err=%v", remote, seq, apErr)
		} else if apErr == nil {
			if ttsAppends <= 15 || ttsAppends%5 == 0 {
				logging.Info("[voice.dialog] phase=tts_append_text_eof chain=6/7 client_remote=%s turn_seq=%d append_idx=%d seg_runes=%d seg=%s",
					remote, seq, ttsAppends, len([]rune(seg)), dialogTruncateForLog(seg))
			}
			if len(ttsSegSample) < 10 {
				ttsSegSample = append(ttsSegSample, seg)
			}
		}
	}

	if len(ttsSegSample) > 0 {
		var sb strings.Builder
		const perSeg = 220
		for i, s := range ttsSegSample {
			if i > 0 {
				sb.WriteString(" ‖ ")
			}
			sb.WriteString(logging.TruncateRunes(strings.TrimSpace(s), perSeg))
		}
		logging.Info("[voice.dialog] phase=tts_enqueue_summary chain=6/7 client_remote=%s turn_seq=%d segments_logged=%d of_total_appends=%d sample_joined=%s",
			remote, seq, len(ttsSegSample), ttsAppends, logging.TruncateRunes(sb.String(), 4000))
	}

	waitErr := stream.Wait()
	if waitErr != nil && streamErr == nil {
		streamErr = fmt.Errorf("llm stream: %w", waitErr)
		logging.Warn("[voice.dialog] llm_wait_err client_remote=%s turn_seq=%d err=%v", remote, seq, waitErr)
	}
	logging.Info("[voice.dialog] tts_finish_send chain=6/7 client_remote=%s turn_seq=%d llm_chunks=%d tts_appends=%d",
		remote, seq, llmChunks, ttsAppends)
	tFin := time.Now()
	if finErr := ttsSession.Finish(); finErr != nil && streamErr == nil {
		streamErr = finErr
		logging.Error("[voice.dialog] tts_finish_failed client_remote=%s turn_seq=%d elapsed=%s err=%v",
			remote, seq, time.Since(tFin).Truncate(time.Millisecond), finErr)
	} else {
		logging.Info("[voice.dialog] tts_finish_ok client_remote=%s turn_seq=%d elapsed=%s",
			remote, seq, time.Since(tFin).Truncate(time.Millisecond))
	}

	tPump := time.Now()
	<-pumpDone
	logging.Info("[voice.dialog] tts_pump_joined chain=7/7 client_remote=%s turn_seq=%d wait_pump=%s",
		remote, seq, time.Since(tPump).Truncate(time.Millisecond))

	final := strings.TrimSpace(assembled.String())
	if streamErr != nil && final == "" {
		return streamErr
	}
	if streamErr != nil {
		logging.Warn("[voice.dialog] turn_partial client_remote=%s turn_seq=%d err=%v assistant_runes=%d",
			remote, seq, streamErr, len([]rune(final)))
	}

	logging.Info("[voice.dialog] phase=assistant_out chain=7/7 client_remote=%s turn_seq=%d final_runes=%d wall_total_ms=%s assistant_full_text=%s",
		remote, seq, len([]rune(final)), time.Since(tAll).Truncate(time.Millisecond), dialogTruncateForLog(final))
	return ww.writeJSON(map[string]any{"op": "assistant", "text": final, "turn_seq": seq})
}

// peelTTSChunks emits phrase-sized prefixes from pending buffer for streaming synthesis.
func peelTTSChunks(pending *string, eof bool) []string {
	const (
		minBeforeStrong       = 3
		minBeforeWeak         = 12
		maxRunesHard          = 56
		strong                = "。！？!?;；\n"
		weak                  = "，,"
	)
	var outs []string
	for {
		if len(strings.TrimSpace(*pending)) == 0 {
			break
		}
		r := []rune(*pending)
		if !eof && len(r) == 0 {
			break
		}
		cut := findTTSCutLen(r, minBeforeStrong, minBeforeWeak, maxRunesHard, strong, weak)
		if cut < 0 {
			break
		}
		seg := strings.TrimSpace(string(r[:cut]))
		*pending = string(r[cut:])
		if seg != "" {
			outs = append(outs, seg)
			continue
		}
		break
	}
	if eof && strings.TrimSpace(*pending) != "" {
		outs = append(outs, strings.TrimSpace(*pending))
		*pending = ""
	}
	return outs
}

func findTTSCutLen(r []rune, minStrong, minWeak, maxHard int, strong string, weak string) int {
	if len(r) == 0 {
		return -1
	}
	first := -1
	for i := minStrong - 1; i < len(r); i++ {
		if strings.ContainsRune(strong, r[i]) {
			first = i + 1
			break
		}
	}
	if first < 0 && len(r) >= minWeak {
		for i := minWeak - 1; i < len(r); i++ {
			if strings.ContainsRune(weak, r[i]) {
				first = i + 1
				break
			}
		}
	}
	if first < 0 && len(r) >= maxHard {
		first = maxHard
	}
	if first <= 0 {
		return -1
	}
	return first
}

func dialogMaskRawQuery(raw string) string {
	v, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if v.Get("api_key") != "" {
		v.Set("api_key", "***")
	}
	return v.Encode()
}
