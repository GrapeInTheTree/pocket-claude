package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

type Message struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Result    string `json:"result,omitempty"`
}

type MessageFile struct {
	Messages []Message `json:"messages"`
}

var (
	inboxPath  string
	outboxPath string
	fileMu     sync.Mutex
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using environment variables")
	}

	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_TOKEN is required")
	}

	chatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	if chatIDStr == "" {
		log.Fatal("TELEGRAM_CHAT_ID is required")
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		log.Fatalf("Invalid TELEGRAM_CHAT_ID: %v", err)
	}

	inboxPath = os.Getenv("INBOX_PATH")
	if inboxPath == "" {
		inboxPath = "./inbox.json"
	}
	outboxPath = os.Getenv("OUTBOX_PATH")
	if outboxPath == "" {
		outboxPath = "./outbox.json"
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Start outbox polling
	go pollOutbox(bot, chatID)

	// Listen for incoming messages
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.Chat.ID != chatID {
			log.Printf("Ignored message from chat %d", update.Message.Chat.ID)
			continue
		}

		msg := Message{
			ID:        fmt.Sprintf("msg_%d", time.Now().Unix()),
			Text:      update.Message.Text,
			Status:    "pending",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		if err := appendToInbox(msg); err != nil {
			log.Printf("Failed to save message: %v", err)
			continue
		}
		log.Printf("Saved message: %s", msg.ID)
	}
}

func readJSONFile(path string) (MessageFile, error) {
	var mf MessageFile

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MessageFile{Messages: []Message{}}, nil
		}
		return mf, err
	}

	if err := json.Unmarshal(data, &mf); err != nil {
		return mf, err
	}
	return mf, nil
}

func writeJSONFile(path string, mf MessageFile) error {
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func appendToInbox(msg Message) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	mf, err := readJSONFile(inboxPath)
	if err != nil {
		return fmt.Errorf("read inbox: %w", err)
	}

	mf.Messages = append(mf.Messages, msg)

	if err := writeJSONFile(inboxPath, mf); err != nil {
		return fmt.Errorf("write inbox: %w", err)
	}
	return nil
}

func pollOutbox(bot *tgbotapi.BotAPI, chatID int64) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := processOutbox(bot, chatID); err != nil {
			log.Printf("Outbox error: %v", err)
		}
	}
}

func processOutbox(bot *tgbotapi.BotAPI, chatID int64) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	mf, err := readJSONFile(outboxPath)
	if err != nil {
		return fmt.Errorf("read outbox: %w", err)
	}

	changed := false
	for i := range mf.Messages {
		if mf.Messages[i].Status != "done" {
			continue
		}

		text := mf.Messages[i].Result
		if text == "" {
			text = "(empty result)"
		}

		msg := tgbotapi.NewMessage(chatID, text)
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message %s: %v", mf.Messages[i].ID, err)
			continue
		}

		mf.Messages[i].Status = "sent"
		changed = true
		log.Printf("Sent outbox message: %s", mf.Messages[i].ID)
	}

	if changed {
		if err := writeJSONFile(outboxPath, mf); err != nil {
			return fmt.Errorf("write outbox: %w", err)
		}
	}
	return nil
}
