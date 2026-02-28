package models

import (
	"testing"
)

func TestModelRequest(t *testing.T) {
	req := &ModelRequest{
		Messages: []Message{
			{Role: "user", Parts: []MessagePart{{Text: "Hello"}}},
		},
	}
	if req.Messages[0].Parts[0].Text != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", req.Messages[0].Parts[0].Text)
	}
}

func TestModelResponse(t *testing.T) {
	resp := &ModelResponse{
		Message: Message{Role: "assistant", Parts: []MessagePart{{Text: "Hi there!"}}},
	}
	if resp.Message.Parts[0].Text != "Hi there!" {
		t.Errorf("Expected 'Hi there!', got '%s'", resp.Message.Parts[0].Text)
	}
}
