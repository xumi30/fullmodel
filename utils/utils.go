package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"fullmodel/utils/fileop"
	"math/rand"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v2"
)

// 将 ReadConfig 改为函数类型的变量
var ReadConfig = func(filename string) (map[string]interface{}, error) {
	// 读取配置文件
	config, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// 解析配置文件
	var data map[string]interface{}
	err = yaml.Unmarshal(config, &data)
	if err != nil {
		return nil, err
	}
	fmt.Println(data)
	return data, nil
}

func GetProviderUrl(providerName string) (string, error) {
	// 读取配置文件
	config, err := ReadConfig(fileop.ResolvePath("config/config.yaml"))
	if err != nil {
		return "", err
	}

	// 获取 providers 数组
	providersList, ok := config["providers"].([]interface{})
	if !ok {
		return "", errors.New("config Providers not found")
	}

	// 遍历 providers 数组，查找匹配的 provider
	for _, p := range providersList {
		provider, ok := p.(map[interface{}]interface{})
		if !ok {
			continue
		}
		if name, ok := provider["name"].(string); ok && name == providerName {
			if url, ok := provider["url"].(string); ok {
				fmt.Println(url)
				return url, nil
			}
		}
	}

	return "", errors.New("config Provider not found")
}

func IsBlank(s string) bool {
	return len(strings.TrimSpace(s)) == 0
}

func normalizePotentialJSONPayload(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "\ufeff")
	raw = strings.TrimSpace(raw)

	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return raw
	}

	trimmedLines := make([]string, 0, len(lines))
	removedDataPrefix := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			removedDataPrefix = true
		}
		if trimmed == "[DONE]" {
			continue
		}
		trimmedLines = append(trimmedLines, trimmed)
	}

	if removedDataPrefix {
		raw = strings.Join(trimmedLines, "\n")
	}

	return strings.TrimSpace(raw)
}

func extractBalancedJSONObject(raw string) string {
	start := -1
	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		c := raw[i]

		if start == -1 {
			if c == '{' {
				start = i
				depth = 1
			}
			continue
		}

		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}

	return ""
}

func ExtractJSON(raw string) string {
	raw = normalizePotentialJSONPayload(raw)

	// 去掉 markdown 包裹
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if balanced := extractBalancedJSONObject(raw); balanced != "" {
		return balanced
	}

	// 找 JSON 区间
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")

	if start >= 0 && end > start {
		return raw[start : end+1]
	}

	return raw
}

// jsonQuoteClosesToken reports whether the double-quote at index i is a JSON string terminator:
// after optional ASCII whitespace, the next byte starts a structural token (: , } ]).
// This distinguishes closing quotes from LLM-typical unescaped " inside string values
// (e.g. 像是"本地"的), which would otherwise break encoding/json.
func jsonQuoteClosesToken(s string, i int) bool {
	if i < 0 || i >= len(s) || s[i] != '"' {
		return false
	}
	j := i + 1
	for j < len(s) {
		switch s[j] {
		case ' ', '\t', '\n', '\r':
			j++
		default:
			switch s[j] {
			case ':', ',', '}', ']':
				return true
			default:
				return false
			}
		}
	}
	return true
}

// RepairUnescapedInnerQuotesInJSONStrings escapes ASCII " that appear inside JSON string values
// but were not written as \". LLMs often emit quotes around Chinese terms in "content"/"reason"
// text, which is invalid JSON and yields errors like invalid character 'æ' (mis-parsed UTF-8).
func RepairUnescapedInnerQuotesInJSONStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	inString := false
	escaped := false

	for i := 0; i < len(s); {
		c := s[i]

		if !inString {
			if c == '"' {
				inString = true
				escaped = false
				b.WriteByte('"')
				i++
				continue
			}
			b.WriteByte(c)
			i++
			continue
		}

		if escaped {
			b.WriteByte(c)
			escaped = false
			i++
			continue
		}
		if c == '\\' {
			b.WriteByte('\\')
			escaped = true
			i++
			continue
		}
		if c != '"' {
			b.WriteByte(c)
			i++
			continue
		}

		// '"' inside string: either real closing quote or unescaped inner quote
		if jsonQuoteClosesToken(s, i) {
			inString = false
			b.WriteByte('"')
			i++
			continue
		}
		b.WriteString(`\"`)
		i++
	}
	return b.String()
}

// EscapeRawNewlinesInJSONStrings replaces literal control characters inside JSON string
// values with escape sequences. LLMs often emit real newlines inside "content" fields,
// which makes the payload invalid for encoding/json.
func EscapeRawNewlinesInJSONStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if inString && c == '\\' {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			b.WriteByte(c)
			continue
		}
		if inString {
			switch c {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteByte(c)
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// PrepareLLMJSON extracts a JSON object region from model output (see ExtractJSON)
// and escapes literal newlines/tabs inside string values. Use before encoding/json.Unmarshal
// for any LLM-produced JSON payload.
func PrepareLLMJSON(raw string) string {
	extracted := ExtractJSON(raw)
	fixed := RepairUnescapedInnerQuotesInJSONStrings(extracted)
	return EscapeRawNewlinesInJSONStrings(fixed)
}

func PreviewJSONBytes(s string, limit int) string {
	if limit <= 0 {
		limit = 64
	}
	b := []byte(s)
	if len(b) > limit {
		b = b[:limit]
	}
	return fmt.Sprintf("len=%d first_bytes=%v quoted=%q", len(s), b, string(b))
}

func UnmarshalLLMJSON(raw string, out interface{}) error {
	candidates := []struct {
		name  string
		value string
	}{
		{name: "raw", value: strings.TrimSpace(raw)},
		{name: "prepared", value: PrepareLLMJSON(raw)},
	}

	var lastErr error
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(candidate.value), out); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("%s parse failed: %w; payload=%s",
				candidate.name, err, PreviewJSONBytes(candidate.value, 96))
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("empty JSON payload")
}

func GenerateChatID() string {
	chatID := fmt.Sprintf("%d%03d", time.Now().UnixMilli(), rand.Intn(1000))
	return chatID
}

func GenerateMessageID() string {

	messageID := fmt.Sprintf("%d%06d", time.Now().UnixMilli(), rand.Intn(100000))
	//logging.Info("Generated messageID: %s", messageID)
	return messageID
}

// SimpleInfoMap builds {"topic": topic, "simpledescription": simpledescription}.
// This lives in utils (not tools) to avoid cross-package coupling.
func SimpleInfoMap(topic, simpledescription string) map[string]string {
	return map[string]string{
		"topic":             topic,
		"simpledescription": simpledescription,
	}
}

const ChatTitleMaxRunes = 15

// TruncateRunes returns s truncated to at most max runes (UTF-8 code points).
// If max <= 0, returns an empty string.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[len(r)-max:])
}
