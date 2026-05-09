// Command voice_clone_http_client calls fullmodel serve POST /v1/voice/customizations (multipart).
//
// Terminal A（需配置 DashScope，与 config 一致）:
//
//	go run ./cmd/fullmodel serve -addr 127.0.0.1:8060
//
// 若启用 HTTP 鉴权，客户端需带同一密钥：
//
//	go run ./examples/voice_clone_http_client -apikey "$FULLMODEL_API_KEY" -audio ./sample.mp3
//
// Terminal B:
//
//	go run ./examples/voice_clone_http_client \
//	  -url "http://127.0.0.1:8060/v1/voice/customizations" \
//	  -audio "./voice.mp3" \
//	  -name "demo_voice"
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:8080/v1/voice/customizations", "HTTP URL for POST /v1/voice/customizations")
	audioPath := flag.String("audio", "", "audio file path (required)")
	name := flag.String("name", "cli_voice", "preferred_name field")
	targetModel := flag.String("target-model", "qwen3-tts-vc-realtime-2026-01-15", "target_model field")
	language := flag.String("language", "zh", "language field")
	apiKey := flag.String("apikey", os.Getenv("FULLMODEL_API_KEY"), "optional X-API-Key when serve auth is on")
	verbose := flag.Bool("verbose", false, "log request/response details")
	flag.Parse()

	if strings.TrimSpace(*audioPath) == "" {
		flag.Usage()
		log.Fatal("-audio is required")
	}
	audioData, err := os.ReadFile(*audioPath)
	if err != nil {
		log.Fatal(err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("preferred_name", *name)
	_ = mw.WriteField("target_model", *targetModel)
	_ = mw.WriteField("language", *language)
	part, err := mw.CreateFormFile("audio", filepath.Base(*audioPath))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := part.Write(audioData); err != nil {
		log.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		log.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, *baseURL, &buf)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if k := strings.TrimSpace(*apiKey); k != "" {
		req.Header.Set("X-API-Key", k)
	}

	if *verbose {
		log.Printf("[voice_clone_client] POST %s audio_file=%s bytes=%d preferred_name=%q target_model=%q language=%q",
			*baseURL, *audioPath, len(audioData), *name, *targetModel, *language)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			log.Fatalf("request: %v — is fullmodel serve listening? e.g. go run ./cmd/fullmodel serve -addr 127.0.0.1:8060", err)
		}
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start).Truncate(time.Millisecond)

	if *verbose {
		snip := string(body)
		if len(snip) > 4000 {
			snip = snip[:4000] + "…(truncated)"
		}
		log.Printf("[voice_clone_client] response HTTP %s elapsed=%s body_len=%d", resp.Status, elapsed, len(body))
		log.Printf("[voice_clone_client] raw_body: %s", snip)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var env struct {
		Status struct {
			Success bool   `json:"success"`
			Error   string `json:"error,omitempty"`
		} `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		log.Fatalf("parse envelope: %v body=%s", err, string(body))
	}
	if !env.Status.Success {
		log.Fatalf("brain status error: %s", env.Status.Error)
	}

	var pretty bytes.Buffer
	_ = json.Indent(&pretty, env.Result, "", "  ")
	fmt.Println(pretty.String())
	log.Printf("[voice_clone_client] done elapsed=%s", elapsed)
}
