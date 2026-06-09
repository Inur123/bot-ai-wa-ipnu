package ai

import (
	"context"
	"log"
	"os"
	"strings"

	"bot-ai-wa-ipnu/internal/knowledge"
	"bot-ai-wa-ipnu/internal/models"
)

func useOpenAI() bool {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER")))
	return provider == "openai-compatible" || provider == "9router"
}

// Init menginisialisasi AI client (Gemini atau OpenAI-Compatible)
func Init(ctx context.Context) error {
	if useOpenAI() {
		return InitOpenAI(ctx)
	}
	return InitGemini(ctx)
}

// Close menutup AI client
func Close() {
	if useOpenAI() {
		CloseOpenAI()
	} else {
		CloseGemini()
	}
}

// Parse mem-parsing pesan natural language menggunakan Gemini/OpenAI-Compatible
func Parse(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	if useOpenAI() {
		log.Printf("[PITI-AI] OpenAI-Compatible memproses pesan dari %s", userID)
		return ParseMessageOpenAI(ctx, rawMessage, userID)
	}
	log.Printf("[PITI-AI] Gemini memproses pesan dari %s", userID)
	return ParseMessageGemini(ctx, rawMessage, userID)
}

// ParseFeedbackAuto mem-parsing feedback/koreksi menggunakan Gemini/OpenAI-Compatible
func ParseFeedbackAuto(ctx context.Context, feedback string, existing *models.Entry) (*models.ParsedEntry, error) {
	if useOpenAI() {
		return ParseFeedbackOpenAI(ctx, feedback, existing)
	}
	return ParseFeedbackGemini(ctx, feedback, existing)
}

// ChatReply membalas pesan secara percakapan menggunakan Gemini/OpenAI-Compatible
func ChatReply(ctx context.Context, message, userID string) (string, error) {
	if !hasKnowledgeSnippet(message) {
		return "Maaf Rekan/Rekanita, saya belum menemukan data yang cukup di knowledge saya.", nil
	}
	if useOpenAI() {
		return ChatOpenAI(ctx, message, userID)
	}
	return ChatGemini(ctx, message, userID)
}

func hasKnowledgeSnippet(message string) bool {
	result := knowledge.SearchWithLimit(message, 2000)
	return strings.Contains(result, "\n--- FILE:")
}
