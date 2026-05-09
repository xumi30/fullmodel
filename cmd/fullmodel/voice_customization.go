package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/agent/brain"
	"github.com/xumi30/fullmodel/utils/logging"
)

type voiceCloneHTTPBody struct {
	PreferredName string `json:"preferred_name"`
	TargetModel   string `json:"target_model"`
	Language      string `json:"language"`
	Text          string `json:"text"`
	Model         string `json:"model"`
	AudioURL      string `json:"audio_url"`
	AudioData     string `json:"audio_data"`
	AudioMimeType string `json:"audio_mime_type"`
	AudioDataURL  string `json:"audio_data_url"`
}

// handleListVoiceCustomizations serves GET /v1/voice/customizations
func handleListVoiceCustomizations(w http.ResponseWriter, r *http.Request, state *serverState) {
	start := time.Now()
	trace := traceForRequest(w, r)
	remote := r.RemoteAddr
	logging.Info("[voice.clone] list_begin trace=%s remote=%s page_size_q=%q page_index_q=%q",
		trace, remote, r.URL.Query().Get("page_size"), r.URL.Query().Get("page_index"))
	if state.sdk == nil {
		logging.Error("[voice.clone] list_reject sdk_nil trace=%s remote=%s", trace, remote)
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("sdk unavailable"))
		return
	}
	q := r.URL.Query()
	pageSize, _ := strconv.Atoi(strings.TrimSpace(q.Get("page_size")))
	pageIndex, _ := strconv.Atoi(strings.TrimSpace(q.Get("page_index")))
	result, err := state.sdk.ListVoices(r.Context(), fullmodel.VoiceListRequest{
		PageSize:  pageSize,
		PageIndex: pageIndex,
	})
	if err != nil {
		logging.Error("[voice.clone] list_upstream_failed trace=%s remote=%s elapsed=%s err=%v", trace, remote, time.Since(start).Truncate(time.Millisecond), err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	n := len(result.Voices)
	logging.Info("[voice.clone] list_ok trace=%s remote=%s voices=%d elapsed=%s request_id=%q", trace, remote, n, time.Since(start).Truncate(time.Millisecond), result.RequestID)
	writeOK(w, result)
}

// handleCreateVoiceCustomization serves POST /v1/voice/customizations (JSON or multipart).
func handleCreateVoiceCustomization(w http.ResponseWriter, r *http.Request, state *serverState) {
	start := time.Now()
	trace := traceForRequest(w, r)
	remote := r.RemoteAddr
	ct := r.Header.Get("Content-Type")
	logging.Info("[voice.clone] create_begin trace=%s remote=%s method=%s content_type=%q content_length=%d client_trace_hdr=%q",
		trace, remote, r.Method, ct, r.ContentLength, r.Header.Get("X-Client-Trace-Id"))
	if state.sdk == nil {
		logging.Error("[voice.clone] create_reject sdk_nil trace=%s remote=%s", trace, remote)
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("sdk unavailable"))
		return
	}
	cloneReq, err := parseVoiceCloneRequest(r)
	if err != nil {
		logging.Warn("[voice.clone] create_parse_failed trace=%s remote=%s elapsed=%s err=%v", trace, remote, time.Since(start).Truncate(time.Millisecond), err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	logging.Info("[voice.clone] create_parsed trace=%s remote=%s preferred_name=%q target_model=%q language=%q model=%q audio=%s text_len=%d",
		trace, remote, cloneReq.PreferredName, cloneReq.TargetModel, cloneReq.Language, cloneReq.Model, voiceCloneAudioSummary(cloneReq), len([]rune(cloneReq.Text)))
	result, err := state.sdk.CloneVoice(r.Context(), cloneReq)
	if err != nil {
		logging.Error("[voice.clone] create_upstream_failed trace=%s remote=%s elapsed=%s err=%v", trace, remote, time.Since(start).Truncate(time.Millisecond), err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	tags := result.AnalysisTags
	logging.Info("[voice.clone] create_ok trace=%s remote=%s voice=%q target_model=%q request_id=%q analysis_tags=%v elapsed=%s",
		trace, remote, result.Voice, result.TargetModel, result.RequestID, tags, time.Since(start).Truncate(time.Millisecond))
	writeOK(w, result)
}

func parseVoiceCloneRequest(r *http.Request) (fullmodel.VoiceCloneRequest, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			return fullmodel.VoiceCloneRequest{}, err
		}
		req := fullmodel.VoiceCloneRequest{
			PreferredName: r.FormValue("preferred_name"),
			TargetModel:   r.FormValue("target_model"),
			Language:      r.FormValue("language"),
			Text:          r.FormValue("text"),
			Model:         r.FormValue("model"),
			AudioDataURL:  strings.TrimSpace(r.FormValue("audio_data_url")),
		}
		audioURL := strings.TrimSpace(r.FormValue("audio_url"))
		if audioURL != "" {
			req.Audio = brain.MediaResource{URL: audioURL, MimeType: r.FormValue("audio_mime_type")}
			return req, nil
		}
		if req.AudioDataURL != "" {
			return req, nil
		}
		file, header, err := r.FormFile("audio")
		if err != nil {
			return fullmodel.VoiceCloneRequest{}, fmt.Errorf("multipart: need audio file, audio_url, or audio_data_url: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return fullmodel.VoiceCloneRequest{}, err
		}
		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		req.Audio = brain.MediaResource{Data: data, MimeType: mimeType}
		return req, nil
	}

	var body voiceCloneHTTPBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return fullmodel.VoiceCloneRequest{}, err
	}
	req := fullmodel.VoiceCloneRequest{
		PreferredName: body.PreferredName,
		TargetModel:   body.TargetModel,
		Language:      body.Language,
		Text:          body.Text,
		Model:         body.Model,
		AudioDataURL:  strings.TrimSpace(body.AudioDataURL),
	}
	if req.AudioDataURL != "" {
		return req, nil
	}
	media, err := mediaFromVoiceCloneJSON(body)
	if err != nil {
		return fullmodel.VoiceCloneRequest{}, err
	}
	req.Audio = media
	return req, nil
}

func mediaFromVoiceCloneJSON(body voiceCloneHTTPBody) (brain.MediaResource, error) {
	if u := strings.TrimSpace(body.AudioURL); u != "" {
		return brain.MediaResource{URL: u, MimeType: body.AudioMimeType}, nil
	}
	if strings.TrimSpace(body.AudioData) == "" {
		return brain.MediaResource{}, fmt.Errorf("voice clone JSON requires audio_url, audio_data (base64), or audio_data_url")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.AudioData))
	if err != nil {
		return brain.MediaResource{}, fmt.Errorf("audio_data base64: %w", err)
	}
	mime := strings.TrimSpace(body.AudioMimeType)
	if mime == "" && len(data) > 0 {
		mime = http.DetectContentType(data)
	}
	return brain.MediaResource{Data: data, MimeType: mime}, nil
}

// handleDeleteVoiceCustomization serves DELETE /v1/voice/customizations/{voice}
func handleDeleteVoiceCustomization(w http.ResponseWriter, r *http.Request, state *serverState) {
	start := time.Now()
	trace := traceForRequest(w, r)
	remote := r.RemoteAddr
	if state.sdk == nil {
		logging.Error("[voice.clone] delete_reject sdk_nil trace=%s remote=%s", trace, remote)
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("sdk unavailable"))
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/v1/voice/customizations/")
	voice, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(voice) == "" {
		logging.Warn("[voice.clone] delete_bad_voice trace=%s remote=%s path=%q err=%v", trace, remote, r.URL.Path, err)
		writeError(w, http.StatusBadRequest, fmt.Errorf("voice id required"))
		return
	}
	logging.Info("[voice.clone] delete_begin trace=%s remote=%s voice=%q", trace, remote, voice)
	requestID, err := state.sdk.DeleteVoice(r.Context(), voice)
	if err != nil {
		logging.Error("[voice.clone] delete_upstream_failed trace=%s remote=%s voice=%q elapsed=%s err=%v", trace, remote, voice, time.Since(start).Truncate(time.Millisecond), err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	logging.Info("[voice.clone] delete_ok trace=%s remote=%s voice=%q request_id=%q elapsed=%s", trace, remote, voice, requestID, time.Since(start).Truncate(time.Millisecond))
	writeOK(w, map[string]any{"request_id": requestID, "voice": voice})
}

func voiceCloneAudioSummary(req fullmodel.VoiceCloneRequest) string {
	if s := strings.TrimSpace(req.AudioDataURL); s != "" {
		return fmt.Sprintf("data_url runes=%d", len([]rune(s)))
	}
	if s := strings.TrimSpace(req.Audio.URL); s != "" {
		return fmt.Sprintf("url %s mime=%q", s, req.Audio.MimeType)
	}
	if len(req.Audio.Data) == 0 {
		return "empty"
	}
	mime := strings.TrimSpace(req.Audio.MimeType)
	if mime == "" {
		mime = "?"
	}
	return fmt.Sprintf("inline bytes=%d mime=%q", len(req.Audio.Data), mime)
}
