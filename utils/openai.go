package utils

import (
	"context"
	"os"

	"github.com/0x9ef/openai-go"
)

func OpenAIBot(prompt string) string {
	e := openai.New(os.Getenv("OPENAI_KEY"))
	r, err := e.Completion(context.Background(), &openai.CompletionOptions{
		// Choose model, you can see list of available models in models.go file
		Model: openai.ModelTextDavinci003,
		// Number of completion tokens to generate response. By default - 1024
		MaxTokens: 1200,
		// Text to completion
		Prompt: []string{prompt},
	})

	if err != nil {
		return "Not available"
	}
	return r.Choices[0].Text
}
