package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"jave/internal/config"
)

// Message is a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client wraps an HTTP client for the Ollama API.
type Client struct {
	httpClient *http.Client
}

// NewClient returns a ready-to-use Client.
func NewClient() *Client {
	return &Client{httpClient: &http.Client{}}
}

// CheckHealth verifies two things:
//  1. Ollama is reachable at config.OllamaAPI
//  2. config.ModelName is pulled and available
//
// Returns a descriptive, actionable error if either check fails.
func (c *Client) CheckHealth() error {
	probeClient := &http.Client{Timeout: 5 * time.Second}

	resp, err := probeClient.Get(config.OllamaAPI + "/api/tags")
	if err != nil {
		return fmt.Errorf(
			"cannot reach Ollama at %s\n"+
				"         Fix : ollama serve   (run in a separate terminal)\n"+
				"       Error : %v",
			config.OllamaAPI, err,
		)
	}
	defer resp.Body.Close()

	var tagsPayload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsPayload); err != nil {
		return fmt.Errorf("could not parse Ollama model list: %w", err)
	}

	for _, m := range tagsPayload.Models {
		// Ollama sometimes stores the name with :latest appended.
		if m.Name == config.ModelName || m.Name == config.ModelName+":latest" ||
			strings.HasPrefix(m.Name, config.ModelName) {
			return nil // healthy
		}
	}

	// Build a readable list of what IS available.
	available := make([]string, 0, len(tagsPayload.Models))
	for _, m := range tagsPayload.Models {
		available = append(available, m.Name)
	}
	availableStr := "(none pulled yet)"
	if len(available) > 0 {
		availableStr = strings.Join(available, ", ")
	}

	return fmt.Errorf(
		"model %q is not pulled\n"+
			"         Fix : ollama pull %s\n"+
			"   Available : %s",
		config.ModelName, config.ModelName, availableStr,
	)
}

// ChatStream sends the full conversation history to Ollama and forwards each
// token to tokenChan. The caller owns and must close tokenChan.
func (c *Client) ChatStream(ctx context.Context, history []Message, tokenChan chan<- string) error {
	requestBody := map[string]interface{}{
		"model":    config.ModelName,
		"messages": history,
		"stream":   true,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		config.OllamaAPI+"/api/chat",
		bytes.NewBuffer(bodyBytes),
	)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf(
			"lost connection to Ollama — is it still running?\n"+
				"  Restart: ollama serve\n"+
				"  Detail : %w",
			err,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		rawBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, rawBody)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode chunk: %w", err)
		}
		if chunk.Message.Content != "" {
			select {
			case tokenChan <- chunk.Message.Content:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if chunk.Done {
			return nil
		}
	}
}