package provider

import (
	"text/template"
)

type AnalysisResponse struct {
	Score      int    `json:"score"`
	Reason     string `json:"reason"`
	IsPhishing bool   `json:"is_phishing"`
	IsSpam     bool   `json:"is_spam"`
}

type AIBase struct {
	model        string
	maxsize      int
	systemPrompt string
	userPrompt   *template.Template
	temperature  *float32
	topP         *float32
	maxTokens    *int32
}
