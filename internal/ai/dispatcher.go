package ai

import (
	"context"

	"bot-ai-wa-ipnu/internal/models"
)

// Init menginisialisasi AI client (9Router / OpenAI-Compatible)
func Init(ctx context.Context) error {
	return InitOpenAI(ctx)
}

// Close menutup AI client
func Close() {
	CloseOpenAI()
}

// Parse mem-parsing pesan natural language menggunakan OpenAI-Compatible / 9Router
func Parse(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	return ParseMessageOpenAI(ctx, rawMessage, userID)
}

// ParseFeedbackAuto mem-parsing feedback/koreksi menggunakan OpenAI-Compatible / 9Router
func ParseFeedbackAuto(ctx context.Context, feedback string, existing *models.Entry) (*models.ParsedEntry, error) {
	return ParseFeedbackOpenAI(ctx, feedback, existing)
}

// ChatReply membalas pesan secara percakapan menggunakan OpenAI-Compatible / 9Router
func ChatReply(ctx context.Context, message, userID string) (string, error) {
	return ChatOpenAI(ctx, message, userID)
}

