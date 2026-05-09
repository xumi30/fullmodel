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

	"github.com/xumi30/fullmodel"
	"github.com/xumi30/fullmodel/agent/brain"
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
	if state.sdk == nil {
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
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, result)
}

// handleCreateVoiceCustomization serves POST /v1/voice/customizations (JSON or multipart).
func handleCreateVoiceCustomization(w http.ResponseWriter, r *http.Request, state *serverState) {
	if state.sdk == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("sdk unavailable"))
		return
	}
	cloneReq, err := parseVoiceCloneRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := state.sdk.CloneVoice(r.Context(), cloneReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
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
	if state.sdk == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("sdk unavailable"))
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/v1/voice/customizations/")
	voice, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(voice) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("voice id required"))
		return
	}
	requestID, err := state.sdk.DeleteVoice(r.Context(), voice)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, map[string]any{"request_id": requestID, "voice": voice})
}
