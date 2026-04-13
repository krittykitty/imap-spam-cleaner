package provider

import (
	"errors"
	"net"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
)

type Provider interface {
	Name() string
	Init(config map[string]string) error
	ValidateConfig(config map[string]string) error
	HealthCheck(config map[string]string) error
	Analyze(message imap.Message) (AnalysisResponse, error)
}

func checkTCP(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

func New(t string) (Provider, error) {
	providers := []Provider{&OpenAI{}, &Ollama{}, &SpamAssassin{}, &Gemini{}}
	for _, provider := range providers {
		if provider.Name() == t {
			return provider, nil
		}
	}
	return nil, errors.New("unknown provider")
}
