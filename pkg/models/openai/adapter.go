package openai

import (
	"encoding/base64"
	"fmt"

	oa "github.com/sashabaranov/go-openai"

	"github.com/seyi/dagens/pkg/models"
)

const (
	partTypeText     = "text"
	partTypeImageURL = "image_url"
)

// toOpenAIMessages converts the internal message representation into the OpenAI SDK structure.
func toOpenAIMessages(msgs []models.Message) ([]oa.ChatCompletionMessage, error) {
	result := make([]oa.ChatCompletionMessage, len(msgs))
	for i, msg := range msgs {
		converted, err := toOpenAIMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("message[%d]: %w", i, err)
		}
		result[i] = converted
	}
	return result, nil
}

func toOpenAIMessage(msg models.Message) (oa.ChatCompletionMessage, error) {
	out := oa.ChatCompletionMessage{
		Role:       string(msg.Role),
		Name:       msg.Name,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
	}

	if len(msg.Parts) > 0 {
		parts, err := toOpenAIParts(msg.Parts)
		if err != nil {
			return out, err
		}
		out.MultiContent = parts
		out.Content = ""
	}

	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = toOpenAIToolCalls(msg.ToolCalls)
	}

	if out.Role == "" {
		return out, fmt.Errorf("message role is required")
	}

	return out, nil
}

func toOpenAIToolCalls(calls []models.ToolCall) []oa.ToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]oa.ToolCall, len(calls))
	for i, call := range calls {
		result[i] = oa.ToolCall{
			ID:   call.ID,
			Type: oa.ToolType(call.Type),
			Function: oa.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}
	}
	return result
}

func toOpenAIParts(parts []models.MessagePart) ([]oa.ChatMessagePart, error) {
	result := make([]oa.ChatMessagePart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case partTypeText, "":
			result = append(result, oa.ChatMessagePart{
				Type: partTypeText,
				Text: part.Text,
			})
		case partTypeImageURL:
			if part.ImageURL == "" {
				return nil, fmt.Errorf("image_url part missing URL")
			}
			result = append(result, oa.ChatMessagePart{
				Type: partTypeImageURL,
				ImageURL: &oa.ChatMessageImageURL{
					URL: part.ImageURL,
				},
			})
		case "image_base64":
			if len(part.ImageData) == 0 {
				return nil, fmt.Errorf("image_base64 part missing data")
			}
			// Use provided MIME type or default to image/png
			mimeType := part.MIMEType
			if mimeType == "" {
				mimeType = "image/png"
			}
			encoded := base64.StdEncoding.EncodeToString(part.ImageData)
			result = append(result, oa.ChatMessagePart{
				Type: partTypeImageURL,
				ImageURL: &oa.ChatMessageImageURL{
					URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded),
				},
			})
		default:
			return nil, fmt.Errorf("unsupported message part type %q", part.Type)
		}
	}
	return result, nil
}

// fromOpenAIMessage converts an OpenAI response message into the internal representation.
func fromOpenAIMessage(msg oa.ChatCompletionMessage) models.Message {
	result := models.Message{
		Role:       models.MessageRole(msg.Role),
		Name:       msg.Name,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
	}

	if len(msg.MultiContent) > 0 {
		result.Parts = fromOpenAIParts(msg.MultiContent)
	}

	if len(msg.ToolCalls) > 0 {
		result.ToolCalls = fromOpenAIToolCalls(msg.ToolCalls)
	}

	if msg.FunctionCall != nil {
		result.ToolCalls = append(result.ToolCalls, models.ToolCall{
			ID:   "",
			Type: "function",
			Function: models.FunctionCall{
				Name:      msg.FunctionCall.Name,
				Arguments: msg.FunctionCall.Arguments,
			},
		})
	}

	return result
}

func fromOpenAIParts(parts []oa.ChatMessagePart) []models.MessagePart {
	result := make([]models.MessagePart, 0, len(parts))
	for _, part := range parts {
		item := models.MessagePart{
			Type: string(part.Type),
			Text: part.Text,
		}

		if part.ImageURL != nil {
			item.Type = partTypeImageURL
			item.ImageURL = part.ImageURL.URL
		}

		result = append(result, item)
	}
	return result
}

func fromOpenAIToolCalls(calls []oa.ToolCall) []models.ToolCall {
	result := make([]models.ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, models.ToolCall{
			ID:   call.ID,
			Type: string(call.Type),
			Function: models.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return result
}

// fromStreamDelta converts a streaming delta into a partial message.
func fromStreamDelta(delta oa.ChatCompletionStreamChoiceDelta) models.Message {
	msg := models.Message{
		Role:    models.MessageRole(delta.Role),
		Content: delta.Content,
	}

	if len(delta.ToolCalls) > 0 {
		msg.ToolCalls = make([]models.ToolCall, 0, len(delta.ToolCalls))
		for _, call := range delta.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, models.ToolCall{
				ID:   call.ID,
				Type: string(call.Type),
				Function: models.FunctionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
	}

	if delta.FunctionCall != nil {
		msg.ToolCalls = append(msg.ToolCalls, models.ToolCall{
			Type: "function",
			Function: models.FunctionCall{
				Name:      delta.FunctionCall.Name,
				Arguments: delta.FunctionCall.Arguments,
			},
		})
	}

	return msg
}

// toOpenAITools converts function/tool definitions into the OpenAI schema.
func toOpenAITools(defs []models.ToolDefinition) []oa.Tool {
	if len(defs) == 0 {
		return nil
	}

	result := make([]oa.Tool, 0, len(defs))
	for _, def := range defs {
		result = append(result, oa.Tool{
			Type: oa.ToolTypeFunction,
			Function: &oa.FunctionDefinition{
				Name:        def.Function.Name,
				Description: def.Function.Description,
				Parameters:  def.Function.Parameters,
			},
		})
	}
	return result
}
