package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	openaiEndpoint    = "https://api.openai.com/v1/chat/completions"
	model             = "gpt-4"
	maxFilesToAnalyze = 10
	maxCharsPerFile   = 8000
	basePrompt        = `You are a CockroachDB expert. Analyze the following file and identify
		inefficiences and anti-patterns. List up to three things. Only include suggestions
		that you are highly confident in being relevant to query performance. Include only the list
		not any summary text beforehand.\n`
)

var prompts = map[string]string{
	"env.sql": basePrompt +
		`Here is the environment file. Answer the following questions if relevant:
		* What version of CockroachDB is being used?
		* What non-default settings are configured?`,
	"plan.txt": basePrompt +
		`Here is the plan.txt file. Answer the following questions if relevant:
		* What are the slowest operations as shown in the plan?
		* What missing indexes might speed up this query?`,
	"statement.sql": basePrompt +
		`Here is the statement file. Answer the following questions if relevant:
		* What are the most common anti-patterns in the query?`,
	"schema.sql": basePrompt +
		`Here is the schema file. Answer the following questions if relevant:
		* What are the most common anti-patterns in the schema?`,
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <statement_bundle.zip>", os.Args[0])
	}
	zipFile := os.Args[1]

	data, err := os.ReadFile(zipFile)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	files, err := unzipInMemory(data)
	if err != nil {
		log.Fatalf("Failed to unzip: %v", err)
	}

	relevant := extractRelevantFiles(files)

	for i, file := range relevant {
		fmt.Printf("🔍 Analyzing file %d of %d: %s...\n\n", i+1, len(relevant), file.Name)
		summary, err := analyzeFile(file.Name, file.Content)
		if err != nil {
			log.Printf("Error analyzing %s: %v", file.Name, err)
			continue
		}
		fmt.Printf("\033[1mSummary for %s:\033[0m\n\n%s\n\n\n", file.Name, summary)
	}
}

type NamedFile struct {
	Name    string
	Content string
}

func unzipInMemory(zipData []byte) (map[string]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}

	files := make(map[string]string)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		buf := new(strings.Builder)
		_, err = io.Copy(buf, rc)
		if err != nil {
			return nil, err
		}

		files[file.Name] = buf.String()
	}
	return files, nil
}

func extractRelevantFiles(files map[string]string) []NamedFile {
	selected := make([]NamedFile, 0, maxFilesToAnalyze)
	for name, content := range files {
		if _, ok := prompts[name]; !ok {
			continue
		}

		if len(content) > maxCharsPerFile {
			content = content[:maxCharsPerFile] + "\n... [truncated]"
		}

		selected = append(selected, NamedFile{Name: name, Content: content})

		if len(selected) >= maxFilesToAnalyze {
			break
		}
	}
	return selected
}

func analyzeFile(name, content string) (string, error) {
	prompt := prompts[name] + "\n\n" + content
	return sendToChatGPT(prompt)
}

func sendToChatGPT(prompt string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a database performance expert."},
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", openaiEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API call failed: %s", bodyBytes)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	return chatResp.Choices[0].Message.Content, nil
}
