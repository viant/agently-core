package openai

import "strings"

// AdaptRequest converts a slice of texts to an OpenAI-specific Request
func AdaptRequest(texts []string, model string) *Request {
	return &Request{
		Model: model,
		Input: texts,
	}
}

// AdaptResponse converts an OpenAI-specific Response to vectors and token count
func AdaptResponse(resp *Response, model string, embeddings *[][]float32, tokens *int) {
	if resp == nil {
		return
	}
	// Preserve caller-provided model when response omits it.
	if strings.TrimSpace(resp.Model) == "" {
		resp.Model = model
	}

	// Extract embeddings from the response
	for _, data := range resp.Data {
		*embeddings = append(*embeddings, data.Embedding)
	}

	// Update token count
	*tokens += resp.Usage.TotalTokens
}
