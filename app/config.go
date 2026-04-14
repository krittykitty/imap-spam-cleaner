package app

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

const configPath = "config.yml"

type Config struct {
	Logging    Logging                    `yaml:"logging"    validate:"required"`
	Defaults   Defaults                   `yaml:"defaults"   validate:"omitempty"`
	Providers  map[string]Provider        `yaml:"providers"  validate:"required,dive"`
	Whitelists map[string][]regexp.Regexp `yaml:"whitelists" validate:"omitempty"`
	Inboxes    []Inbox                    `yaml:"inboxes"    validate:"required,dive"`
}

type Defaults struct {
	SystemPrompt              string `yaml:"system_prompt"`
	UserPrompt                string `yaml:"user_prompt"`
	ConsolidationSystemPrompt string `yaml:"consolidation_system_prompt"`
	ConsolidationUserPrompt   string `yaml:"consolidation_user_prompt"`
	ConsolidationPrompt       string `yaml:"consolidation_prompt"`
}

type Logging struct {
	Level string `yaml:"level" validate:"omitempty"`
}

type Provider struct {
	Type        string            `yaml:"type"   validate:"required,oneof=openai ollama spamassassin gemini"`
	Concurrency int               `yaml:"concurrency" validate:"omitempty,min=1"`
	RateLimit   float64           `yaml:"rate_limit" validate:"omitempty"`
	Config      map[string]string `yaml:"config" validate:"required"`
}

const DefaultIdleTimeout = 25 * time.Minute

type Inbox struct {
	Schedule                    string        `yaml:"schedule"     validate:"required_without=EnableIdle"`
	Host                        string        `yaml:"host"         validate:"required"`
	Port                        int           `yaml:"port"         validate:"required"`
	TLS                         bool          `yaml:"tls"          validate:"omitempty"`
	Username                    string        `yaml:"username"     validate:"required"`
	Password                    string        `yaml:"password"     validate:"required"`
	Provider                    string        `yaml:"provider"     validate:"required"`
	Inbox                       string        `yaml:"inbox"        validate:"required"`
	Spam                        string        `yaml:"spam"         validate:"required"`
	MinScore                    int           `yaml:"minscore"     validate:"required,gte=0,lte=100"`
	MinAge                      time.Duration `yaml:"minage"       validate:"omitempty"`
	MaxAge                      time.Duration `yaml:"maxage"       validate:"omitempty"`
	Whitelist                   string        `yaml:"whitelist"    validate:"omitempty"`
	EnableIdle                  bool          `yaml:"enable_idle"  validate:"omitempty"`
	IdleTimeout                 time.Duration `yaml:"idle_timeout" validate:"omitempty"`
	EnableSentWhitelist         bool          `yaml:"enable_sent_whitelist" validate:"omitempty"`
	SentFolder                  string        `yaml:"sent_folder"  validate:"omitempty"`
	SentFolderMaxAge            time.Duration `yaml:"sent_folder_maxage" validate:"omitempty"`
	SentFolderSchedule          string        `yaml:"sent_folder_schedule" validate:"omitempty"`
	RecentConsolidationEvery    int           `yaml:"recent_consolidation_every" validate:"omitempty,min=1"`
	RecentConsolidationInterval time.Duration `yaml:"recent_consolidation_interval" validate:"omitempty"`
	ConsolidationProvider       string        `yaml:"consolidation_provider" validate:"omitempty"`
	ForceInitialPopulation      bool          `yaml:"force_initial_population" validate:"omitempty"`
	MaxRetries                  *int          `yaml:"max_retries" validate:"omitempty,min=0"`
}

func LoadConfig() (*Config, error) {

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err = yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Apply top-level defaults to providers if not explicitly set
	for name, prov := range config.Providers {
		if prov.Config == nil {
			prov.Config = make(map[string]string)
		}
		if config.Defaults.SystemPrompt != "" && prov.Config["system_prompt"] == "" {
			prov.Config["system_prompt"] = config.Defaults.SystemPrompt
		}
		if config.Defaults.UserPrompt != "" && prov.Config["user_prompt"] == "" {
			prov.Config["user_prompt"] = config.Defaults.UserPrompt
		}
		if config.Defaults.ConsolidationSystemPrompt != "" && prov.Config["consolidation_system_prompt"] == "" {
			prov.Config["consolidation_system_prompt"] = config.Defaults.ConsolidationSystemPrompt
		}
		if config.Defaults.ConsolidationUserPrompt != "" && prov.Config["consolidation_user_prompt"] == "" {
			prov.Config["consolidation_user_prompt"] = config.Defaults.ConsolidationUserPrompt
		}
		if config.Defaults.ConsolidationPrompt != "" && prov.Config["consolidation_prompt"] == "" {
			prov.Config["consolidation_prompt"] = config.Defaults.ConsolidationPrompt
		}
		// provider-level defaults
		if prov.Concurrency <= 0 {
			prov.Concurrency = 1
		}
		if prov.RateLimit < 0 {
			prov.RateLimit = 0
		}
		config.Providers[name] = prov
	}

	if err = validator.New().Struct(&config); err != nil {
		return nil, err
	}

	if config.Logging.Level != "" {
		logx.SetLevel(config.Logging.Level)
	}

	for i, inbox := range config.Inboxes {
		if _, ok := config.Providers[inbox.Provider]; !ok {
			return nil, fmt.Errorf("invalid provider %s for inbox #%d", inbox.Provider, i)
		}
		if inbox.ConsolidationProvider != "" {
			if _, ok := config.Providers[inbox.ConsolidationProvider]; !ok {
				return nil, fmt.Errorf("invalid consolidation provider %s for inbox #%d", inbox.ConsolidationProvider, i)
			}
		}
		if _, ok := config.Whitelists[inbox.Whitelist]; inbox.Whitelist != "" && !ok {
			return nil, fmt.Errorf("invalid whitelist %s for inbox #%d", inbox.Whitelist, i)
		}
	}

	for i := range config.Inboxes {
		if config.Inboxes[i].EnableSentWhitelist {
			if config.Inboxes[i].SentFolder == "" {
				config.Inboxes[i].SentFolder = "Sent"
			}
			if config.Inboxes[i].SentFolderMaxAge == 0 {
				config.Inboxes[i].SentFolderMaxAge = 2160 * time.Hour
			}
			if config.Inboxes[i].SentFolderSchedule == "" {
				config.Inboxes[i].SentFolderSchedule = "0 * * * *"
			}
		}
		if config.Inboxes[i].RecentConsolidationEvery == 0 {
			config.Inboxes[i].RecentConsolidationEvery = 50
		}
		if config.Inboxes[i].RecentConsolidationInterval == 0 {
			config.Inboxes[i].RecentConsolidationInterval = 24 * time.Hour
		}
		if config.Inboxes[i].MaxRetries == nil {
			v := 3
			config.Inboxes[i].MaxRetries = &v
		}
	}

	logx.Debug("Loaded config")

	return &config, nil
}
