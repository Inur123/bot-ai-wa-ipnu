package gemini

import (
	"context"
	"log"
	"os"

	"bot-ai-wa-ipnu/internal/models"
)

// Parse adalah fungsi utama yang memilih AI provider secara otomatis
// berdasarkan env AI_PROVIDER. Gunakan ini di mana saja, bukan ParseMessage langsung.
func Parse(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	provider := os.Getenv("AI_PROVIDER")
	switch provider {
	case "groq":
		log.Printf("[PITI-AI] Groq (Llama 3.1) memproses pesan dari %s", userID)
		return ParseMessageGroq(ctx, rawMessage, userID)
	case "huggingface", "hf":
		log.Printf("[PITI-AI] HuggingFace memproses pesan dari %s", userID)
		return ParseMessageHF(ctx, rawMessage, userID)
	default:
		// Gemini atau OFFLINE_MODE
		return ParseMessage(ctx, rawMessage, userID)
	}
}

// ParseFeedbackAuto memilih AI provider untuk parsing feedback secara otomatis
func ParseFeedbackAuto(ctx context.Context, feedback string, existing *models.Entry) (*models.ParsedEntry, error) {
	provider := os.Getenv("AI_PROVIDER")
	switch provider {
	case "groq":
		return ParseFeedbackGroq(ctx, feedback, existing)
	case "huggingface", "hf":
		return ParseFeedbackHF(ctx, feedback, existing)
	default:
		return ParseFeedback(ctx, feedback, existing)
	}
}

// ChatReply membalas pesan secara percakapan (tanpa format JSON).
func ChatReply(ctx context.Context, message, userID string) (string, error) {
	provider := os.Getenv("AI_PROVIDER")
	switch provider {
	case "groq":
		return ChatGroq(ctx, message, userID)
	case "huggingface", "hf":
		return ChatHF(ctx, message, userID)
	default:
		return ChatGemini(ctx, message, userID)
	}
}
