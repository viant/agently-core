package ollama

import "strings"

// AdaptRequest converts a generic embedder.Request to an Ollama-specific Request
func AdaptRequest(texts []string, model string) []*Request {
	var result []*Request
	for i := range texts {
		result = append(result, &Request{
			Model:  model,
			Prompt: texts[i],
		})
	}
	return result
}

// AdaptResponse converts an Ollama-specific Response to a generic embedder.Response
func AdaptResponse(resp *Response, model string, embeddings *[][]float32, tokens *int) {
	if resp == nil {
		return
	}
	// Preserve caller-provided model when response omits it.
	if strings.TrimSpace(resp.Model) == "" {
		resp.Model = model
	}
	*embeddings = append(*embeddings, resp.Embedding)
	*tokens += resp.EvalCount
}
