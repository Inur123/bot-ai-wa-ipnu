package main

import (
	"encoding/json"
	"fmt"
	"log"

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/models"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file")
	}

	if err := database.Connect(); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}

	rows, err := database.DB.Query("SELECT id, type, content, metadata, status, created_at FROM entries ORDER BY id DESC LIMIT 5")
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e models.Entry
		var metaRaw []byte
		err := rows.Scan(&e.ID, &e.Type, &e.Content, &metaRaw, &e.Status, &e.CreatedAt)
		if err != nil {
			log.Fatalf("Scan failed: %v", err)
		}
		json.Unmarshal(metaRaw, &e.Metadata)
		fmt.Printf("Entry ID: #%d\n", e.ID)
		fmt.Printf("Type: %s\n", e.Type)
		fmt.Printf("Status: %s\n", e.Status)
		fmt.Printf("Triggers:\n")
		for _, t := range e.Metadata.Triggers {
			fmt.Printf("  - ID: %s, SendAt: %s, Status: %s\n", t.ID, t.SendAt, t.Status)
		}
		fmt.Println("-------------------")
	}
}
