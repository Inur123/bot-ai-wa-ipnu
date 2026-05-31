package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/models"
)

// ParseMessageHF mem-parsing pesan natural language menggunakan Hugging Face API
func ParseMessageHF(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	hfToken := os.Getenv("HUGGINGFACE_TOKEN")
	if hfToken == "" {
		return nil, fmt.Errorf("HUGGINGFACE_TOKEN tidak diset di .env")
	}

	now := time.Now()
	systemPrompt := buildSystemPrompt(now)

	// Model dan endpoint yang digunakan (standard serverless API - tidak perlu permission khusus)
	hfModel := os.Getenv("HUGGINGFACE_MODEL")
	if hfModel == "" {
		hfModel = "mistralai/Mistral-7B-Instruct-v0.3"
	}
	apiURL := fmt.Sprintf("https://api-inference.huggingface.co/models/%s/v1/chat/completions", hfModel)

	// Persiapkan body request ke Hugging Face Inference API (OpenAI compatible format)
	reqBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("Pesan dari user (ID: %s):\n%s", userID, rawMessage)},
		},
		"temperature": 0.1,
		"max_tokens":  800,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gagal marshal request: %w", err)
	}

	// Buat HTTP request ke Hugging Face Serverless Inference API
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		apiURL,
		bytes.NewBuffer(jsonBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+hfToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal mengirim request ke Hugging Face: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respon: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	// Parsing struktur response OpenAI compatible
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("gagal unmarshal response: %w\nRaw: %s", err, string(respBytes))
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("respon Hugging Face kosong")
	}

	rawJSON := chatResp.Choices[0].Message.Content
	rawJSON = strings.TrimSpace(rawJSON)

	// Bersihkan markdown codeblock (```json ... ```) jika ditambahkan oleh model LLM
	rawJSON = cleanJSONString(rawJSON)

	log.Printf("[HuggingFace] Raw response: %s", rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON terstruktur: %w\nRaw: %s", err, rawJSON)
	}

	// Set status awal trigger ke pending
	for i := range parsed.Metadata.Triggers {
		if parsed.Metadata.Triggers[i].ID == "" {
			parsed.Metadata.Triggers[i].ID = fmt.Sprintf("t%d", i+1)
		}
		parsed.Metadata.Triggers[i].Status = models.StatusPending
	}
	parsed.Metadata.UserID = userID

	return &parsed, nil
}

// ParseFeedbackHF mem-parsing feedback menggunakan Hugging Face API
func ParseFeedbackHF(ctx context.Context, feedback string, existingEntry *models.Entry) (*models.ParsedEntry, error) {
	hfToken := os.Getenv("HUGGINGFACE_TOKEN")
	if hfToken == "" {
		return nil, fmt.Errorf("HUGGINGFACE_TOKEN tidak diset di .env")
	}

	now := time.Now()
	existingJSON, _ := json.MarshalIndent(existingEntry, "", "  ")

	systemPrompt := fmt.Sprintf(`
Kamu adalah AI assistant yang membantu update entry pesan/reminder.
Waktu sekarang: %s

Entry yang ada saat ini:
%s

Tugas kamu: Update entry tersebut sesuai feedback dari user. Kembalikan HANYA JSON dalam format terstruktur yang sama dengan entry yang ada.
Pastikan field "type", "content", dan "metadata" selalu ada.
Jangan ubah data yang tidak disebutkan dalam feedback.
`, now.Format("2006-01-02 15:04 MST"), string(existingJSON))

	hfModel := os.Getenv("HUGGINGFACE_MODEL")
	if hfModel == "" {
		hfModel = "mistralai/Mistral-7B-Instruct-v0.3"
	}
	apiURL := fmt.Sprintf("https://api-inference.huggingface.co/models/%s/v1/chat/completions", hfModel)

	reqBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("Pesan feedback/koreksi: %s", feedback)},
		},
		"temperature": 0.1,
		"max_tokens":  800,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gagal marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		apiURL,
		bytes.NewBuffer(jsonBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+hfToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal mengirim request ke Hugging Face: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respon: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("gagal unmarshal response: %w\nRaw: %s", err, string(respBytes))
	}

	rawJSON := chatResp.Choices[0].Message.Content
	rawJSON = cleanJSONString(rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal feedback response: %w\nRaw: %s", err, rawJSON)
	}

	return &parsed, nil
}

// ChatHF membalas percakapan biasa menggunakan Hugging Face API
func ChatHF(ctx context.Context, message, userID string) (string, error) {
	hfToken := os.Getenv("HUGGINGFACE_TOKEN")
	if hfToken == "" {
		return "", fmt.Errorf("HUGGINGFACE_TOKEN tidak diset di .env")
	}

	hfModel := os.Getenv("HUGGINGFACE_MODEL")
	if hfModel == "" {
		hfModel = "mistralai/Mistral-7B-Instruct-v0.3"
	}
	apiURL := fmt.Sprintf("https://api-inference.huggingface.co/models/%s/v1/chat/completions", hfModel)

	systemPrompt := "Kamu adalah PITI, asisten AI IPNU-IPPNU Magetan. Jawab singkat, jelas, ramah, dan dalam Bahasa Indonesia. Jangan gunakan format JSON."

	reqBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"temperature": 0.4,
		"max_tokens":  400,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gagal marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("gagal membuat request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+hfToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal mengirim request ke Hugging Face: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gagal membaca respon: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Hugging Face API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("gagal unmarshal response: %w\nRaw: %s", err, string(respBytes))
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("respon Hugging Face kosong")
	}

	return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
}

// cleanJSONString membersihkan markdown code block jika ada
func cleanJSONString(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}
