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
	openaiEndpoint = "https://api.openai.com/v1/chat/completions"
	model          = "gpt-4"
	basePrompt     = `You are a CockroachDB expert. Analyze the following
		files and identify inefficiences and anti-patterns. Only include
		suggestions that you are highly confident in being relevant to query
		performance. Include only the list not any summary text beforehand.

		* What are the slowest operations as shown in the plan?
		* What are the most common anti-patterns in the schema?
		* What are the most common anti-patterns in the query?
		* What missing indexes might speed up this query?
	`
)

// fileNames is the list of files to use for analysis.
var fileNames = [...]string{"schema.sql", "statement.sql", "plan.txt"}

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

	fmt.Printf("🔍 Analyzing statement bundle...\n\n")
	prompt := buildPrompt(files)
	response, err := sendToChatGPT(prompt)
	if err != nil {
		log.Fatalf("API error: %v\n", err)
	}
	fmt.Print(response)
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

func buildPrompt(files map[string]string) string {
	var buf bytes.Buffer
	buf.WriteString(basePrompt)
	for _, name := range fileNames {
		if content, ok := files[name]; ok {
			buf.WriteString(content)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func sendToChatGPT(prompt string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	reqBody := request{
		Model: model,
		Messages: []message{
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

	var chatResp response
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	return chatResp.Choices[0].Message.Content, nil
}
