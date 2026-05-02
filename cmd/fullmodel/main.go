package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fullmodel/agent/brain"
	agentruntime "fullmodel/agent/runtime"
	agenttools "fullmodel/agent/tools"
	"fullmodel/processmessage"
	"fullmodel/utils/fileop"
)

type messageRequest struct {
	Kind      processmessage.Kind `json:"kind"`
	SessionID string              `json:"session_id,omitempty"`
	System    string              `json:"system,omitempty"`
	Prompt    string              `json:"prompt,omitempty"`
	Text      string              `json:"text,omitempty"`
	Stream    bool                `json:"stream,omitempty"`
	Messages  []brain.Message     `json:"messages,omitempty"`
	Media     struct {
		Image      mediaJSON `json:"image,omitempty"`
		Audio      mediaJSON `json:"audio,omitempty"`
		Video      mediaJSON `json:"video,omitempty"`
		FirstFrame mediaJSON `json:"first_frame,omitempty"`
	} `json:"media,omitempty"`
	Options processmessage.Options `json:"options,omitempty"`
}

type mediaJSON struct {
	URL      string `json:"url,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type messageResponse struct {
	Mode         brain.BrainMode           `json:"mode,omitempty"`
	Text         string                    `json:"text,omitempty"`
	ImageURL     string                    `json:"image_url,omitempty"`
	AudioBase64  string                    `json:"audio_base64,omitempty"`
	AudioMime    string                    `json:"audio_mime,omitempty"`
	VideoURL     string                    `json:"video_url,omitempty"`
	Artifacts    []agentruntime.Artifact   `json:"artifacts,omitempty"`
	ToolCalls    []brain.ToolCall          `json:"tool_calls,omitempty"`
	Metadata     map[string]any            `json:"metadata,omitempty"`
	Usage        *brain.Usage              `json:"usage,omitempty"`
	Capabilities []agentruntime.Capability `json:"capabilities,omitempty"`
}

type envelope struct {
	ID     string            `json:"id"`
	Status brain.BrainStatus `json:"status"`
	Result any               `json:"result,omitempty"`
	Error  *apiError         `json:"error,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
}

type serverState struct {
	runner    *agentruntime.Runner
	sessions  agentruntime.SessionMemory
	tasks     *agentruntime.TaskStore
	artifacts *agentruntime.ArtifactStore
	tools     *agentruntime.ToolRegistryExecutor
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
			os.Exit(1)
		}
	case "run":
		if err := runOnce(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(2)
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:8080", "listen address")
	configFile := flags.String("config", "", "config file path")
	apiKey := flags.String("api-key", os.Getenv("FULLMODEL_API_KEY"), "API key for HTTP auth; empty disables auth")
	taskWorkers := flags.Int("task-workers", 2, "number of background task workers")
	if err := flags.Parse(args); err != nil {
		return err
	}

	state, err := newServerState(*configFile, *taskWorkers)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, messageResponse{Capabilities: state.runner.Registry().Capabilities()})
	})
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		handleMessage(w, r, state)
	})
	mux.HandleFunc("POST /v1/chat", func(w http.ResponseWriter, r *http.Request) {
		req := messageRequest{Kind: processmessage.KindText}
		if err := decodeMessageRequest(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Kind == "" {
			req.Kind = processmessage.KindText
		}
		runMessage(w, r, state, req)
	})
	mux.HandleFunc("POST /v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		handleCreateTask(w, r, state)
	})
	mux.HandleFunc("GET /v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, state.tasks.List())
	})
	mux.HandleFunc("DELETE /v1/tasks/", func(w http.ResponseWriter, r *http.Request) {
		handleCancelTask(w, r, state)
	})
	mux.HandleFunc("GET /v1/tasks/", func(w http.ResponseWriter, r *http.Request) {
		handleGetTask(w, r, state)
	})
	mux.HandleFunc("GET /v1/artifacts", func(w http.ResponseWriter, r *http.Request) {
		_ = state.artifacts.CleanupExpired()
		writeOK(w, state.artifacts.List())
	})
	mux.HandleFunc("GET /v1/artifacts/", func(w http.ResponseWriter, r *http.Request) {
		handleGetArtifact(w, r, state)
	})
	mux.HandleFunc("GET /v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		handleGetSession(w, r, state)
	})
	mux.HandleFunc("DELETE /v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteSession(w, r, state)
	})
	mux.HandleFunc("GET /v1/tools", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, state.tools.Tools())
	})

	fmt.Printf("fullmodel api listening on http://%s\n", *addr)
	return http.ListenAndServe(*addr, authMiddleware(mux, *apiKey))
}

func runOnce(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	configFile := flags.String("config", "", "config file path")
	kind := flags.String("kind", "text", "message kind")
	prompt := flags.String("prompt", "", "prompt/text")
	stream := flags.Bool("stream", false, "stream output for text")
	if err := flags.Parse(args); err != nil {
		return err
	}

	runner, err := newRunner(*configFile)
	if err != nil {
		return err
	}
	req := messageRequest{
		Kind:   processmessage.Kind(*kind),
		Prompt: *prompt,
		Text:   *prompt,
		Stream: *stream,
	}
	msg, opts, err := buildMessage(req, agentruntime.NewSessionStore())
	if err != nil {
		return err
	}
	result, err := runner.Run(context.Background(), agentruntime.Request{Message: msg, Options: opts})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(responseFromOutput(result.Output))
}

func newServerState(configFile string, taskWorkers int) (*serverState, error) {
	var (
		cfgs *fileop.BrainConfigs
		err  error
	)
	if strings.TrimSpace(configFile) != "" {
		cfgs, err = fileop.LoadBrainConfigsFromFile(configFile)
	} else {
		cfgs, err = fileop.LoadBrainConfigs()
	}
	if err != nil {
		return nil, err
	}
	registry, err := agentruntime.NewRegistryFromConfigs(cfgs)
	if err != nil {
		return nil, err
	}
	toolExecutor := agentruntime.NewToolRegistryExecutor(agenttools.Getregistry())
	artifactStore, err := agentruntime.NewArtifactStoreWithOptions(agentruntime.ArtifactStoreOptions{
		Root:     filepath.Join(fileop.RuntimeRoot(), "data", "artifacts"),
		MaxBytes: 128 << 20,
		TTL:      7 * 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	sessionStore, err := agentruntime.NewFileSessionStore(filepath.Join(fileop.RuntimeRoot(), "data", "sessions"))
	if err != nil {
		return nil, err
	}
	return &serverState{
		runner:    agentruntime.NewRunner(registry, toolExecutor),
		sessions:  sessionStore,
		tasks:     agentruntime.NewTaskStore(taskWorkers),
		artifacts: artifactStore,
		tools:     toolExecutor,
	}, nil
}

func newRunner(configFile string) (*agentruntime.Runner, error) {
	state, err := newServerState(configFile, 2)
	if err != nil {
		return nil, err
	}
	return state.runner, nil
}

func handleMessage(w http.ResponseWriter, r *http.Request, state *serverState) {
	var req messageRequest
	if err := decodeMessageRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runMessage(w, r, state, req)
}

func runMessage(w http.ResponseWriter, r *http.Request, state *serverState, req messageRequest) {
	msg, opts, err := buildMessage(req, state.sessions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := state.runner.Run(r.Context(), agentruntime.Request{Message: msg, Options: opts})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if result.Output != nil && result.Output.Stream != nil {
		writeStream(w, result.Output.Stream)
		return
	}
	rememberSession(req, state.sessions, result.Output)
	resp, err := responseFromResult(result, state.artifacts, agentruntime.ArtifactMeta{
		SessionID: req.SessionID,
		Source:    "message",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, resp)
}

func buildMessage(req messageRequest, sessions agentruntime.SessionMemory) (processmessage.Message, processmessage.Options, error) {
	opts := req.Options
	opts.Stream = req.Stream || opts.Stream
	switch req.Kind {
	case "", processmessage.KindText:
		text := firstNonEmpty(req.Text, req.Prompt)
		return processmessage.TextMessage{
			Text:    text,
			System:  req.System,
			History: sessions.Messages(req.SessionID),
		}, opts, nil
	case processmessage.KindChat:
		return processmessage.ChatMessage{Messages: req.Messages}, opts, nil
	case processmessage.KindImage:
		media, err := decodeMedia(req.Media.Image)
		return processmessage.ImageMessage{Prompt: req.Prompt, Image: media}, opts, err
	case processmessage.KindVideo:
		media, err := decodeMedia(req.Media.Video)
		return processmessage.VideoMessage{Prompt: req.Prompt, Video: media}, opts, err
	case processmessage.KindSpeechToText:
		media, err := decodeMedia(req.Media.Audio)
		return processmessage.SpeechToTextMessage{Audio: media}, opts, err
	case processmessage.KindTextToSpeech:
		return processmessage.TextToSpeechMessage{Text: firstNonEmpty(req.Text, req.Prompt)}, opts, nil
	case processmessage.KindImageGenerate:
		return processmessage.ImageGenerateMessage{Prompt: req.Prompt}, opts, nil
	case processmessage.KindImageEdit:
		media, err := decodeMedia(req.Media.Image)
		return processmessage.ImageEditMessage{Prompt: req.Prompt, Images: []brain.MediaResource{media}}, opts, err
	case processmessage.KindTextToVideo:
		return processmessage.TextToVideoMessage{Prompt: req.Prompt}, opts, nil
	case processmessage.KindImageToVideo:
		media, err := decodeMedia(req.Media.FirstFrame)
		return processmessage.ImageToVideoMessage{Prompt: req.Prompt, FirstFrame: media}, opts, err
	default:
		return nil, opts, fmt.Errorf("unsupported kind %q", req.Kind)
	}
}

func rememberSession(req messageRequest, sessions agentruntime.SessionMemory, out *brain.BrainOutput) {
	if sessions == nil || req.SessionID == "" || out == nil || !out.Status.Success {
		return
	}
	if req.Kind != "" && req.Kind != processmessage.KindText {
		return
	}
	userText := firstNonEmpty(req.Text, req.Prompt)
	if strings.TrimSpace(userText) != "" {
		sessions.Append(req.SessionID, brain.NewUserMessage(userText))
	}
	if assistant := strings.TrimSpace(out.Content.Text); assistant != "" {
		sessions.Append(req.SessionID, brain.NewAssistantMessage(assistant))
	}
}

func decodeMedia(in mediaJSON) (brain.MediaResource, error) {
	if strings.TrimSpace(in.URL) != "" {
		return brain.MediaResource{URL: in.URL, MimeType: in.MimeType}, nil
	}
	if strings.TrimSpace(in.Data) == "" {
		return brain.MediaResource{}, nil
	}
	data, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil {
		return brain.MediaResource{}, err
	}
	return brain.MediaResource{Data: data, MimeType: in.MimeType}, nil
}

func responseFromResult(result *agentruntime.Result, artifacts *agentruntime.ArtifactStore, artifactMeta agentruntime.ArtifactMeta) (messageResponse, error) {
	if result == nil {
		return messageResponse{}, nil
	}
	resp := responseFromOutput(result.Output)
	resp.ToolCalls = result.ToolCalls
	if result.Output != nil && len(result.Output.Content.Audio.Data) > 0 && artifacts != nil {
		artifact, err := artifacts.SaveWithMeta(result.Output.Content.Audio.Data, firstNonEmpty(result.Output.Content.Audio.MimeType, "audio/mpeg"), artifactMeta)
		if err != nil {
			return messageResponse{}, err
		}
		resp.Artifacts = append(resp.Artifacts, artifact)
		resp.AudioBase64 = ""
	}
	return resp, nil
}

func responseFromOutput(out *brain.BrainOutput) messageResponse {
	if out == nil {
		return messageResponse{}
	}
	resp := messageResponse{
		Mode:     out.Mode,
		Text:     out.Content.Text,
		ImageURL: out.Content.Image.URL,
		VideoURL: out.Content.Video.URL,
		Metadata: out.Metadata,
		Usage:    out.Usage,
	}
	if len(out.Content.Audio.Data) > 0 {
		resp.AudioBase64 = base64.StdEncoding.EncodeToString(out.Content.Audio.Data)
		resp.AudioMime = firstNonEmpty(out.Content.Audio.MimeType, "audio/mpeg")
	}
	return resp
}

func decodeMessageRequest(r *http.Request, req *messageRequest) error {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return decodeMultipartMessageRequest(r, req)
	}
	return json.NewDecoder(r.Body).Decode(req)
}

func decodeMultipartMessageRequest(r *http.Request, req *messageRequest) error {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return err
	}
	req.Kind = processmessage.Kind(r.FormValue("kind"))
	req.SessionID = r.FormValue("session_id")
	req.System = r.FormValue("system")
	req.Prompt = r.FormValue("prompt")
	req.Text = r.FormValue("text")
	req.Stream = r.FormValue("stream") == "true"

	for field, target := range map[string]*mediaJSON{
		"image":       &req.Media.Image,
		"audio":       &req.Media.Audio,
		"video":       &req.Media.Video,
		"first_frame": &req.Media.FirstFrame,
	} {
		media, ok, err := multipartMedia(r, field)
		if err != nil {
			return err
		}
		if ok {
			*target = media
		}
	}
	return nil
}

func multipartMedia(r *http.Request, field string) (mediaJSON, bool, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		if err == http.ErrMissingFile {
			return mediaJSON{}, false, nil
		}
		return mediaJSON{}, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return mediaJSON{}, false, err
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return mediaJSON{
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}, true, nil
}

func handleCreateTask(w http.ResponseWriter, r *http.Request, state *serverState) {
	var req messageRequest
	if err := decodeMessageRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	msg, opts, err := buildMessage(req, state.sessions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := state.tasks.StartWithOptions(context.Background(), state.runner, agentruntime.Request{Message: msg, Options: opts}, agentruntime.TaskOptions{
		Kind:      string(req.Kind),
		SessionID: req.SessionID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, task)
}

func handleGetTask(w http.ResponseWriter, r *http.Request, state *serverState) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	task, ok := state.tasks.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("task %s not found", id))
		return
	}
	if task.Status == agentruntime.TaskSucceeded && task.Result != nil && len(task.Artifacts) == 0 {
		resp, err := responseFromResult(task.Result, state.artifacts, agentruntime.ArtifactMeta{
			SessionID: task.SessionID,
			TaskID:    task.ID,
			Source:    "task",
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if len(resp.Artifacts) > 0 {
			if updated, ok := state.tasks.SetArtifacts(task.ID, resp.Artifacts); ok {
				task = updated
			}
		}
	}
	writeOK(w, task)
}

func handleCancelTask(w http.ResponseWriter, r *http.Request, state *serverState) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	task, ok := state.tasks.Cancel(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("task %s not found", id))
		return
	}
	writeOK(w, task)
}

func handleGetArtifact(w http.ResponseWriter, r *http.Request, state *serverState) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/artifacts/")
	artifact, ok := state.artifacts.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("artifact %s not found", id))
		return
	}
	w.Header().Set("Content-Type", artifact.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(artifact.Path)))
	http.ServeFile(w, r, artifact.Path)
}

func handleGetSession(w http.ResponseWriter, r *http.Request, state *serverState) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	writeOK(w, map[string]any{
		"id":       id,
		"messages": state.sessions.Messages(id),
	})
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request, state *serverState) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	state.sessions.Clear(id)
	writeOK(w, map[string]any{"id": id, "cleared": true})
}

func writeStream(w http.ResponseWriter, stream brain.StreamOutput) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case text, ok := <-stream.Text():
			if !ok {
				if err := stream.Wait(); err != nil {
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				}
				fmt.Fprint(w, "event: done\ndata: {}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			data, _ := json.Marshal(map[string]string{"text": text})
			fmt.Fprintf(w, "event: delta\ndata: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}
		case err, ok := <-stream.Error():
			if ok && err != nil {
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				return
			}
		}
	}
}

func authMiddleware(next http.Handler, apiKey string) http.Handler {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorized(r, apiKey) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
	})
}

func authorized(r *http.Request, apiKey string) bool {
	if r.Header.Get("X-API-Key") == apiKey {
		return true
	}
	const bearer = "Bearer "
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, bearer) && strings.TrimSpace(strings.TrimPrefix(auth, bearer)) == apiKey
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOK(w http.ResponseWriter, result any) {
	id, _ := agentruntimeID()
	writeJSON(w, http.StatusOK, envelope{
		ID:     id,
		Status: brain.BrainStatus{Success: true},
		Result: result,
	})
}

func writeError(w http.ResponseWriter, status int, err error) {
	id, _ := agentruntimeID()
	writeJSON(w, status, envelope{
		ID:     id,
		Status: brain.BrainStatus{Success: false, Error: err.Error()},
		Error:  &apiError{Message: err.Error()},
	})
}

func agentruntimeID() (string, error) {
	return agentruntime.RandomPublicID("req")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func printUsage() {
	fmt.Print(`usage:
  fullmodel serve [-addr 127.0.0.1:8080] [-config config/llm.yaml]
  fullmodel run [-kind text] [-prompt "..."] [-stream]
`)
}
