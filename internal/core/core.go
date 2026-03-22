package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/spf13/viper"
)

// MediaType represents the type of media content.
type MediaType string

const (
	MediaTypeImage    MediaType = "image"
	MediaTypeVideo    MediaType = "video"
	MediaTypeAudio    MediaType = "audio"
	MediaTypeText     MediaType = "text"
	MediaTypeDocument MediaType = "document"
	MediaTypeUnknown  MediaType = "unknown"
)

type ItemStatus string

const (
	StatusPending    ItemStatus = "pending"
	StatusProcessing ItemStatus = "processing"
	StatusReady      ItemStatus = "ready"
	StatusFailed     ItemStatus = "failed"
)

type MediaItem struct {
	ID         string                 `json:"id"`
	SourcePath string                 `json:"source_path"`
	MediaType  MediaType              `json:"media_type"`
	MimeType   string                 `json:"mime_type,omitempty"`
	FileSize   int64                  `json:"file_size"`
	FileHash   string                 `json:"file_hash,omitempty"`
	Status     ItemStatus             `json:"status"`
	ErrorMsg   string                 `json:"error_msg,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

type Summary struct {
	ID         string   `json:"id"`
	MediaID    string   `json:"media_id"`
	Summary    string   `json:"summary"`
	KeyPoints  []string `json:"key_points"`
	Tags       []string `json:"tags"`
	AIProvider string   `json:"ai_provider"`
	Model      string   `json:"model"`
	TokenCount int      `json:"token_count"`
	CreatedAt  string   `json:"created_at"`
}

type Classification struct {
	ID         string   `json:"id"`
	MediaID    string   `json:"media_id"`
	Topic      string   `json:"topic"`
	Tags       []string `json:"tags"`
	Confidence float64  `json:"confidence"`
	AIProvider string   `json:"ai_provider"`
	Model      string   `json:"model"`
	CreatedAt  string   `json:"created_at"`
}

type TextChunk struct {
	ID         string `json:"id"`
	MediaID    string `json:"media_id"`
	ChunkIndex int    `json:"chunk_index"`
	ChunkText  string `json:"chunk_text"`
	TokenCount int    `json:"token_count"`
	CreatedAt  string `json:"created_at"`
}

type Embedding struct {
	ID        string    `json:"id"`
	ChunkID   string    `json:"chunk_id"`
	Embedding []float32 `json:"embedding"`
	CreatedAt string    `json:"created_at"`
}

type SearchResult struct {
	MediaItem *MediaItem `json:"media_item"`
	Summary   *Summary   `json:"summary,omitempty"`
	ChunkText string     `json:"chunk_text"`
	Score     float64    `json:"score"`
	ChunkID   string     `json:"chunk_id"`
}

type Config struct {
	Database DatabaseConfig `json:"database"`
	AI       AIConfig      `json:"ai"`
	App      AppConfig     `json:"app"`
	Ingest   IngestConfig  `json:"ingest"`
	Watch    WatchConfig   `json:"watch"`
}

type DatabaseConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Name     string `json:"name"`
	MaxConns int    `json:"max_conns"`
	MinConns int    `json:"min_conns"`
}

type AIConfig struct {
	Provider string         `json:"provider"`
	Minimax  MinimaxConfig  `json:"minimax,omitempty"`
	OpenAI   OpenAIConfig   `json:"openai,omitempty"`
	Ollama   OllamaConfig   `json:"ollama,omitempty"`
	Codex    CodexConfig    `json:"codex,omitempty"`
}

type CodexConfig struct {
	APIKey         string `json:"api_key"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	EmbeddingModel string `json:"embedding_model"`
	EmbeddingDim   int    `json:"embedding_dim"`
	ReasoningEffort string `json:"reasoning_effort"` // high, medium, low (for Responses API)
}

type MinimaxConfig struct {
	APIKey          string  `json:"api_key"`
	BaseURL          string  `json:"base_url"`  // OpenAI-compatible endpoint, e.g. https://api.minimaxi.com/v1
	Model           string  `json:"model"`  // MiniMax-M2.7, MiniMax-M2-her, MiniMax-M2.5, etc.
	EmbeddingModel  string  `json:"embedding_model"`  // text-embedding-3-small or "" to skip
	EmbeddingDim   int     `json:"embedding_dim"`
	MaxTokens      int     `json:"max_tokens"`
	Temperature    float64 `json:"temperature"`
	ReasoningSplit bool    `json:"reasoning_split"`
}

type OpenAIConfig struct {
	APIKey          string `json:"api_key"`
	BaseURL          string `json:"base_url"`
	Model           string `json:"model"`
	EmbeddingModel  string `json:"embedding_model"`
	EmbeddingDim    int    `json:"embedding_dim"`
}

type OllamaConfig struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	EmbeddingModel string `json:"embedding_model"`
}

type AppConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DataDir  string `json:"data_dir"`
	LogLevel string `json:"log_level"`
}

type IngestConfig struct {
	ChunkSize      int      `json:"chunk_size"`
	ChunkOverlap   int      `json:"chunk_overlap"`
	MaxFileSize    string   `json:"max_file_size"`
	SupportedTypes []string `json:"supported_types"`
}

type WatchConfig struct {
	Enabled  bool     `json:"enabled"`
	Paths    []string `json:"paths"`
	Debounce string   `json:"debounce"`
}

// LoadConfig loads configuration from config.yaml and environment variables.
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		// Try current directory first
		v.AddConfigPath(".")
		// Try common locations
		v.AddConfigPath("./kb")
		v.AddConfigPath("/home/gongchao/gastown/kb")
		// Try home directory
		home, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(home + "/.config/kb")
		}
		v.SetConfigName("config")
		v.SetConfigType("yaml")
	}
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	// Unmarshal each section separately to avoid viper's case-sensitivity issues
	cfg.Database.Host = v.GetString("database.host")
	cfg.Database.Port = v.GetInt("database.port")
	cfg.Database.User = v.GetString("database.user")
	cfg.Database.Password = v.GetString("database.password")
	cfg.Database.Name = v.GetString("database.name")
	cfg.Database.MaxConns = v.GetInt("database.max_conns")
	cfg.Database.MinConns = v.GetInt("database.min_conns")

	cfg.AI.Provider = v.GetString("ai.provider")
	cfg.AI.Minimax.APIKey = v.GetString("ai.minimax.api_key")
	cfg.AI.Minimax.BaseURL = v.GetString("ai.minimax.base_url")
	cfg.AI.Minimax.Model = v.GetString("ai.minimax.model")
	cfg.AI.Minimax.EmbeddingModel = v.GetString("ai.minimax.embedding_model")
	cfg.AI.Minimax.EmbeddingDim = v.GetInt("ai.minimax.embedding_dim")
	cfg.AI.Minimax.MaxTokens = v.GetInt("ai.minimax.max_tokens")
	cfg.AI.Minimax.Temperature = v.GetFloat64("ai.minimax.temperature")
	cfg.AI.Minimax.ReasoningSplit = v.GetBool("ai.minimax.reasoning_split")
	cfg.AI.OpenAI.APIKey = v.GetString("ai.openai.api_key")
	cfg.AI.OpenAI.BaseURL = v.GetString("ai.openai.base_url")
	cfg.AI.OpenAI.Model = v.GetString("ai.openai.model")
	cfg.AI.OpenAI.EmbeddingModel = v.GetString("ai.openai.embedding_model")
	cfg.AI.OpenAI.EmbeddingDim = v.GetInt("ai.openai.embedding_dim")
	cfg.AI.Ollama.BaseURL = v.GetString("ai.ollama.base_url")
	cfg.AI.Ollama.Model = v.GetString("ai.ollama.model")
	cfg.AI.Ollama.EmbeddingModel = v.GetString("ai.ollama.embedding_model")
	cfg.AI.Codex.APIKey = v.GetString("ai.codex.api_key")
	cfg.AI.Codex.BaseURL = v.GetString("ai.codex.base_url")
	cfg.AI.Codex.Model = v.GetString("ai.codex.model")
	cfg.AI.Codex.EmbeddingModel = v.GetString("ai.codex.embedding_model")
	cfg.AI.Codex.EmbeddingDim = v.GetInt("ai.codex.embedding_dim")
	cfg.AI.Codex.ReasoningEffort = v.GetString("ai.codex.reasoning_effort")

	cfg.App.Host = v.GetString("app.host")
	cfg.App.Port = v.GetInt("app.port")
	cfg.App.DataDir = v.GetString("app.data_dir")
	cfg.App.LogLevel = v.GetString("app.log_level")

	cfg.Ingest.ChunkSize = v.GetInt("ingest.chunk_size")
	cfg.Ingest.ChunkOverlap = v.GetInt("ingest.chunk_overlap")
	cfg.Ingest.MaxFileSize = v.GetString("ingest.max_file_size")
	cfg.Ingest.SupportedTypes = v.GetStringSlice("ingest.supported_types")

	cfg.Watch.Enabled = v.GetBool("watch.enabled")
	cfg.Watch.Paths = v.GetStringSlice("watch.paths")
	cfg.Watch.Debounce = v.GetString("watch.debounce")

	cfg.resolveEnv()
	if cfg.Database.Host == "" {
		cfg.Database.Host = "localhost"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.MaxConns == 0 {
		cfg.Database.MaxConns = 10
	}
	if cfg.Database.MinConns == 0 {
		cfg.Database.MinConns = 2
	}
	if cfg.AI.Minimax.EmbeddingDim == 0 {
		cfg.AI.Minimax.EmbeddingDim = 1024
	}
	if cfg.AI.Minimax.MaxTokens == 0 {
		cfg.AI.Minimax.MaxTokens = 4096
	}
	if cfg.AI.Minimax.Temperature == 0 {
		cfg.AI.Minimax.Temperature = 0.7
	}
	if cfg.AI.Minimax.Model == "" {
		cfg.AI.Minimax.Model = "abab5.5-chat"
	}
	if cfg.AI.Minimax.EmbeddingModel == "" {
		cfg.AI.Minimax.EmbeddingModel = "embo01"
	}
	if cfg.AI.Codex.BaseURL == "" {
		cfg.AI.Codex.BaseURL = "https://api.codex-for.me/v1"
	}
	if cfg.AI.Codex.Model == "" {
		cfg.AI.Codex.Model = "gpt-4.1-mini"
	}
	if cfg.AI.Codex.EmbeddingModel == "" {
		cfg.AI.Codex.EmbeddingModel = "text-embedding-3-small"
	}
	if cfg.AI.Codex.EmbeddingDim == 0 {
		cfg.AI.Codex.EmbeddingDim = 1536
	}
	if cfg.App.Port == 0 {
		cfg.App.Port = 8080
	}
	if cfg.App.LogLevel == "" {
		cfg.App.LogLevel = "info"
	}
	if cfg.Ingest.ChunkSize == 0 {
		cfg.Ingest.ChunkSize = 512
	}
	if cfg.Ingest.ChunkOverlap == 0 {
		cfg.Ingest.ChunkOverlap = 64
	}
	if cfg.Ingest.MaxFileSize == "" {
		cfg.Ingest.MaxFileSize = "100MB"
	}
	return &cfg, nil
}

func (c *Config) resolveEnv() {
	c.Database.Host = resolveEnvOrValue(os.Getenv("KB_DB_HOST"), c.Database.Host)
	if v := os.Getenv("KB_DB_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &c.Database.Port)
	}
	c.Database.User = resolveEnvOrValue(os.Getenv("KB_DB_USER"), c.Database.User)
	c.Database.Password = resolveEnvOrValue(os.Getenv("KB_DB_PASSWORD"), c.Database.Password)
	c.Database.Name = resolveEnvOrValue(os.Getenv("KB_DB_NAME"), c.Database.Name)
	c.AI.Provider = resolveEnvOrValue(os.Getenv("KB_AI_PROVIDER"), c.AI.Provider)
	c.AI.Minimax.APIKey = resolveEnvOrValue(os.Getenv("MINIMAX_API_KEY"), c.AI.Minimax.APIKey)
	c.AI.Minimax.BaseURL = resolveEnvOrValue(os.Getenv("MINIMAX_BASE_URL"), c.AI.Minimax.BaseURL)
	c.AI.Minimax.Model = resolveEnvOrValue(os.Getenv("MINIMAX_MODEL"), c.AI.Minimax.Model)
	c.AI.Minimax.EmbeddingModel = resolveEnvOrValue(os.Getenv("MINIMAX_EMBEDDING_MODEL"), c.AI.Minimax.EmbeddingModel)
	if v := os.Getenv("MINIMAX_EMBEDDING_DIM"); v != "" {
		fmt.Sscanf(v, "%d", &c.AI.Minimax.EmbeddingDim)
	}
	if v := os.Getenv("MINIMAX_MAX_TOKENS"); v != "" {
		fmt.Sscanf(v, "%d", &c.AI.Minimax.MaxTokens)
	}
	if v := os.Getenv("MINIMAX_TEMPERATURE"); v != "" {
		fmt.Sscanf(v, "%f", &c.AI.Minimax.Temperature)
	}

	c.AI.OpenAI.APIKey = resolveEnvOrValue(os.Getenv("OPENAI_API_KEY"), c.AI.OpenAI.APIKey)
	c.AI.OpenAI.BaseURL = resolveEnvOrValue(os.Getenv("OPENAI_BASE_URL"), c.AI.OpenAI.BaseURL)
	c.AI.OpenAI.Model = resolveEnvOrValue(os.Getenv("OPENAI_MODEL"), c.AI.OpenAI.Model)
	c.AI.OpenAI.EmbeddingModel = resolveEnvOrValue(os.Getenv("OPENAI_EMBEDDING_MODEL"), c.AI.OpenAI.EmbeddingModel)
	if v := os.Getenv("OPENAI_EMBEDDING_DIM"); v != "" {
		fmt.Sscanf(v, "%d", &c.AI.OpenAI.EmbeddingDim)
	}

	c.AI.Codex.APIKey = resolveEnvOrValue(os.Getenv("CODEX_API_KEY"), c.AI.Codex.APIKey)
	c.AI.Codex.BaseURL = resolveEnvOrValue(os.Getenv("CODEX_BASE_URL"), c.AI.Codex.BaseURL)
	c.AI.Codex.Model = resolveEnvOrValue(os.Getenv("CODEX_MODEL"), c.AI.Codex.Model)
	c.AI.Codex.EmbeddingModel = resolveEnvOrValue(os.Getenv("CODEX_EMBEDDING_MODEL"), c.AI.Codex.EmbeddingModel)
	if v := os.Getenv("CODEX_EMBEDDING_DIM"); v != "" {
		fmt.Sscanf(v, "%d", &c.AI.Codex.EmbeddingDim)
	}
	c.App.Host = resolveEnvOrValue(os.Getenv("KB_HOST"), c.App.Host)
	if v := os.Getenv("KB_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &c.App.Port)
	}
	c.App.DataDir = resolveEnvOrValue(os.Getenv("KB_DATA_DIR"), c.App.DataDir)
	c.App.LogLevel = resolveEnvOrValue(os.Getenv("KB_LOG_LEVEL"), c.App.LogLevel)
}

func resolveEnvOrValue(env, value string) string {
	if env != "" {
		return env
	}
	return value
}

func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.Name)
}

func (c *IngestConfig) IsSupportedType(ext string) bool {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	for _, t := range c.SupportedTypes {
		if strings.EqualFold(t, ext) {
			return true
		}
	}
	return false
}

type AIProvider interface {
	Summarize(ctx context.Context, text string) (*Summary, error)
	Classify(ctx context.Context, text string) (*Classification, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Name() string
}

type MinimaxProvider struct {
	client     LLMClient
	model      string
	embedModel string
	embedDim   int
	maxTokens  int
	temp       float64
	name       string
}

type CodexProvider struct {
	client          *goOpenAIClient
	model           string
	embedModel      string
	embedDim        int
	maxTokens       int
	temp            float64
	name            string
	reasoningEffort string
}

func NewCodexProvider(apiKey, baseURL, model, embedModel string, embedDim, maxTokens int, temp float64, reasoningEffort string) *CodexProvider {
	if baseURL == "" {
		baseURL = "https://api.codex-for.me/v1"
	}
	if model == "" {
		model = "gpt-4.1-mini"
	}
	if embedModel == "" {
		embedModel = "text-embedding-3-small"
	}
	if embedDim == 0 {
		embedDim = 1536
	}
	if maxTokens == 0 {
		maxTokens = 2048
	}
	if temp == 0 {
		temp = 0.7
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	client := newGoOpenAIClient(apiKey, baseURL, model).WithHTTPClient(httpClient)
	return &CodexProvider{
		client:          client,
		model:           model,
		embedModel:      embedModel,
		embedDim:        embedDim,
		maxTokens:       maxTokens,
		temp:            temp,
		name:            "codex",
		reasoningEffort: reasoningEffort,
	}
}

func (p *CodexProvider) Name() string { return p.name }

type responsesCreateRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	MaxOutput      int    `json:"max_output_tokens,omitempty"`
	Temp           float64 `json:"temperature,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type responsesCreateResponse struct {
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (p *CodexProvider) createResponse(ctx context.Context, input string, maxOutput int, temp float64) (string, error) {
	reqBody := responsesCreateRequest{
		Model:          p.model,
		Input:          input,
		MaxOutput:      maxOutput,
		Temp:           temp,
		ReasoningEffort: p.reasoningEffort,
	}
	body, _ := json.Marshal(reqBody)

	httpReq, err := newHTTPRequest("POST", p.client.baseURL+"/responses", body)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.client.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doRequestWithClient[responsesCreateResponse](ctx, p.client.httpClient, httpReq)
	if err != nil {
		return "", fmt.Errorf("codex responses: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", fmt.Errorf("codex responses error: %s", resp.Error.Message)
	}
	if strings.TrimSpace(resp.OutputText) != "" {
		return resp.OutputText, nil
	}
	if len(resp.Output) == 0 {
		return "", fmt.Errorf("codex responses: empty output")
	}
	var b strings.Builder
	for _, o := range resp.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" || c.Type == "text" {
				if c.Text != "" {
					b.WriteString(c.Text)
				}
			}
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("codex responses: empty output text")
	}
	return out, nil
}

func (p *CodexProvider) Summarize(ctx context.Context, text string) (*Summary, error) {
	if countTokens(text) > 3500 {
		runes := []rune(text)
		text = string(runes[:intMin(len(runes), 12000)])
	}
	prompt := strings.Replace(summarizationPrompt, "{{.Content}}", text, 1)
	out, err := p.createResponse(ctx, prompt, p.maxTokens, p.temp)
	if err != nil {
		return nil, err
	}

	var result struct {
		Summary   string   `json:"summary"`
		KeyPoints []string `json:"key_points"`
		Tags      []string `json:"tags"`
	}
	jsonText, err := parseJSONResponse(out)
	if err != nil {
		result.Summary = out
	} else if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	return &Summary{
		Summary:    result.Summary,
		KeyPoints: result.KeyPoints,
		Tags:      result.Tags,
		AIProvider: p.name,
		Model:      p.model,
		TokenCount: countTokens(text),
	}, nil
}

func (p *CodexProvider) Classify(ctx context.Context, text string) (*Classification, error) {
	if len(text) > 2000 {
		runes := []rune(text)
		text = string(runes[:2000])
	}
	prompt := strings.Replace(classificationPrompt, "{{.Content}}", text, 1)
	out, err := p.createResponse(ctx, prompt, 512, 0.3)
	if err != nil {
		return nil, err
	}

	var result struct {
		Topic      string   `json:"topic"`
		Tags       []string `json:"tags"`
		Confidence float64  `json:"confidence"`
	}
	jsonText, err := parseJSONResponse(out)
	if err != nil {
		result.Topic = "未分类"
		result.Tags = []string{}
		result.Confidence = 0
	} else if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if result.Confidence == 0 && result.Topic != "" {
		result.Confidence = 0.8
	}
	return &Classification{
		Topic:      result.Topic,
		Tags:       result.Tags,
		Confidence: result.Confidence,
		AIProvider: p.name,
		Model:      p.model,
	}, nil
}

func (p *CodexProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	truncated := make([]string, len(texts))
	for i, t := range texts {
		if countTokens(t) > 2000 {
			runes := []rune(t)
			truncated[i] = string(runes[:intMin(len(runes), 8000)])
		} else {
			truncated[i] = t
		}
	}
	resp, err := p.client.CreateEmbeddings(ctx, EmbeddingRequest{
		Input: truncated,
		Model: p.embedModel,
	})
	if err != nil {
		return nil, fmt.Errorf("codex embed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		result[i] = d.Embedding
	}
	return result, nil
}

// OpenAIClient is an interface over the go-openai client to allow for easy swapping.
type LLMClient interface {
	CreateChatCompletion(ctx context.Context, model string, req ChatCompletionRequest) (*ChatCompletionResponse, error)
	CreateEmbeddings(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)
}

// ChatCompletionRequest matches go-openai's request format.
type ChatCompletionRequest struct {
	Model       string                    `json:"model"`
	Messages    []map[string]string        `json:"messages"`
	MaxTokens   int                       `json:"max_tokens,omitempty"`
	Temperature float64                   `json:"temperature,omitempty"`
	ExtraBody  map[string]any             `json:"extra_body,omitempty"`
}

// ChatCompletionResponse matches go-openai's response format.
type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
		Index int         `json:"index"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens     int `json:"total_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// EmbeddingRequest matches go-openai's request format.
type EmbeddingRequest struct {
	Input any    `json:"input"`
	Model string `json:"model"`
}

// EmbeddingResponse matches go-openai's response format.
type EmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
		Index    int       `json:"index"`
	} `json:"data"`
	Model  string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type goOpenAIClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

func newGoOpenAIClient(apiKey, baseURL, model string) *goOpenAIClient {
	return &goOpenAIClient{apiKey: apiKey, baseURL: baseURL, model: model, httpClient: http.DefaultClient}
}

func (c *goOpenAIClient) WithHTTPClient(httpClient *http.Client) *goOpenAIClient {
	if httpClient == nil {
		return c
	}
	cpy := *c
	cpy.httpClient = httpClient
	return &cpy
}

func (c *goOpenAIClient) CreateChatCompletion(ctx context.Context, model string, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if model == "" {
		model = c.model
	}
	body, _ := json.Marshal(req)
	httpReq, err := newHTTPRequest("POST", c.baseURL+"/chat/completions", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return doRequestWithClient[ChatCompletionResponse](ctx, httpClient, httpReq)
}

func (c *goOpenAIClient) CreateEmbeddings(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := newHTTPRequest("POST", c.baseURL+"/embeddings", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return doRequestWithClient[EmbeddingResponse](ctx, httpClient, httpReq)
}

func newHTTPRequest(method, url string, body []byte) (*http.Request, error) {
	return http.NewRequest(method, url, bytes.NewReader(body))
}

func doRequest[T any](ctx context.Context, req *http.Request) (*T, error) {
	return doRequestWithClient[T](ctx, http.DefaultClient, req)
}

func doRequestWithClient[T any](ctx context.Context, client *http.Client, req *http.Request) (*T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if len(b) == 0 {
			return nil, fmt.Errorf("http %s: %s", req.URL.String(), resp.Status)
		}
		return nil, fmt.Errorf("http %s: %s: %s", req.URL.String(), resp.Status, strings.TrimSpace(string(b)))
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func NewMinimaxProvider(apiKey, baseURL, model, embedModel string, embedDim, maxTokens int, temp float64) *MinimaxProvider {
	if model == "" {
		model = "MiniMax-M2.7"
	}
	if embedDim == 0 {
		embedDim = 1536
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}
	if temp == 0 {
		temp = 1.0
	}
	if embedModel == "" {
		embedModel = "text-embedding-3-small"
	}

	client := newGoOpenAIClient(apiKey, baseURL, model)
	return &MinimaxProvider{
		client:     client,
		model:      model,
		embedModel: embedModel,
		embedDim:   embedDim,
		maxTokens:   maxTokens,
		temp:       temp,
		name:       "minimax",
	}
}

func (p *MinimaxProvider) Name() string { return p.name }

func (p *MinimaxProvider) Summarize(ctx context.Context, text string) (*Summary, error) {
	if countTokens(text) > 3500 {
		runes := []rune(text)
		text = string(runes[:intMin(len(runes), 12000)])
	}

	prompt := strings.Replace(summarizationPrompt, "{{.Content}}", text, 1)

	resp, err := p.client.CreateChatCompletion(ctx, p.model, ChatCompletionRequest{
		Model: p.model,
		Messages: []map[string]string{
			{"role": "system", "content": "You are a helpful assistant. Always respond in JSON format."},
			{"role": "user", "content": prompt},
		},
		MaxTokens:   p.maxTokens,
		Temperature: p.temp,
	})
	if err != nil {
		return nil, fmt.Errorf("minimax chat: %w", err)
	}

	var result struct {
		Summary   string   `json:"summary"`
		KeyPoints []string `json:"key_points"`
		Tags      []string `json:"tags"`
	}

	var reply string
	if len(resp.Choices) > 0 {
		reply = resp.Choices[0].Message.Content
	}

	jsonText, err := parseJSONResponse(reply)
	if err != nil {
		result.Summary = reply
	} else if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	return &Summary{
		Summary:    result.Summary,
		KeyPoints: result.KeyPoints,
		Tags:       result.Tags,
		AIProvider: p.name,
		Model:      p.model,
		TokenCount: countTokens(text),
	}, nil
}

func (p *MinimaxProvider) Classify(ctx context.Context, text string) (*Classification, error) {
	if len(text) > 2000 {
		runes := []rune(text)
		text = string(runes[:2000])
	}

	prompt := strings.Replace(classificationPrompt, "{{.Content}}", text, 1)

	resp, err := p.client.CreateChatCompletion(ctx, p.model, ChatCompletionRequest{
		Model: p.model,
		Messages: []map[string]string{
			{"role": "system", "content": "You are a classification assistant. Respond in JSON format."},
			{"role": "user", "content": prompt},
		},
		MaxTokens:   256,
		Temperature: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("minimax classify: %w", err)
	}

	var result struct {
		Topic      string   `json:"topic"`
		Tags       []string `json:"tags"`
		Confidence float64  `json:"confidence"`
	}

	var reply string
	if len(resp.Choices) > 0 {
		reply = resp.Choices[0].Message.Content
	}

	jsonText, err := parseJSONResponse(reply)
	if err != nil {
		result.Topic = "未分类"
		result.Tags = []string{}
		result.Confidence = 0
	} else if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if result.Confidence == 0 && result.Topic != "" {
		result.Confidence = 0.8
	}
	return &Classification{
		Topic:      result.Topic,
		Tags:       result.Tags,
		Confidence: result.Confidence,
		AIProvider: p.name,
		Model:      p.model,
	}, nil
}

func (p *MinimaxProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	truncated := make([]string, len(texts))
	for i, t := range texts {
		if countTokens(t) > 2000 {
			runes := []rune(t)
			truncated[i] = string(runes[:intMin(len(runes), 8000)])
		} else {
			truncated[i] = t
		}
	}
	resp, err := p.client.CreateEmbeddings(ctx, EmbeddingRequest{
		Input: truncated,
		Model: p.embedModel,
	})
	if err != nil {
		return nil, fmt.Errorf("minimax embed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		result[i] = d.Embedding
	}
	return result, nil
}

const summarizationPrompt = `你是一个知识助手。请对以下内容进行总结：

内容：
{{.Content}}

请提供：
1. 简要总结（2-3句话）
2. 3-5个关键要点（用列表格式）
3. 3-5个标签（用逗号分隔）

格式要求：JSON格式输出，如下：
{
  "summary": "简要总结文本",
  "key_points": ["要点1", "要点2", "要点3"],
  "tags": ["标签1", "标签2", "标签3"]
}`

const classificationPrompt = `你是一个知识分类助手。请分析以下内容并分类：

内容：
{{.Content}}

请确定：
1. 主要主题/领域（1个）
2. 3-5个标签

格式要求：JSON格式输出，如下：
{
  "topic": "主要主题",
  "tags": ["标签1", "标签2", "标签3"],
  "confidence": 0.85
}`

func countTokens(text string) int {
	chinese, english, other := 0, 0, 0
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Han) || r == '\n' || r == ' ' || unicode.IsPunct(r):
			chinese++
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			english++
		default:
			other++
		}
	}
	return (chinese+other)/2 + english/4
}

func parseJSONResponse(text string) (string, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```json") {
		text = text[7:]
	} else if strings.HasPrefix(text, "```") {
		text = text[3:]
	}
	if strings.HasSuffix(text, "```") {
		text = text[:len(text)-3]
	}
	text = strings.TrimSpace(text)
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start == -1 || end == -1 {
		return "", fmt.Errorf("no JSON found")
	}
	return text[start : end+1], nil
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// === Store ===

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(ctx context.Context, dsn string, maxConns, minConns int) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = int32(minConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()  { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) CreateMediaItem(ctx context.Context, item *MediaItem) error {
	meta, _ := json.Marshal(item.Metadata)
	_, err := s.pool.Exec(ctx, `INSERT INTO media_items (id, source_path, media_type, mime_type, file_size, file_hash, status, metadata) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		item.ID, item.SourcePath, item.MediaType, item.MimeType, item.FileSize, item.FileHash, item.Status, meta)
	return err
}

func (s *Store) GetMediaItem(ctx context.Context, id string) (*MediaItem, error) {
	var item MediaItem
	var meta []byte
	err := s.pool.QueryRow(ctx, `SELECT id,source_path,media_type,mime_type,file_size,file_hash,status,error_msg,metadata,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM media_items WHERE id=$1`,
		id).Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &meta, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if meta != nil {
		json.Unmarshal(meta, &item.Metadata)
	}
	return &item, nil
}

func (s *Store) GetMediaItemByHash(ctx context.Context, hash string) (*MediaItem, error) {
	var item MediaItem
	err := s.pool.QueryRow(ctx, `SELECT id,source_path,media_type,mime_type,file_size,file_hash,status,error_msg,metadata,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM media_items WHERE file_hash=$1`,
		hash).Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &item.Metadata, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &item, err
}

func (s *Store) UpdateMediaItemStatus(ctx context.Context, id string, status ItemStatus, errMsg string) error {
	_, err := s.pool.Exec(ctx, `UPDATE media_items SET status=$2, error_msg=$3 WHERE id=$1`, id, status, errMsg)
	return err
}

func (s *Store) ListMediaItems(ctx context.Context, mediaType, status string, limit, offset int) ([]*MediaItem, int, error) {
	where := "1=1"
	args := []any{}
	idx := 1
	if mediaType != "" {
		where += fmt.Sprintf(" AND media_type=$%d", idx)
		args, idx = append(args, mediaType), idx+1
	}
	if status != "" {
		where += fmt.Sprintf(" AND status=$%d", idx)
		args, idx = append(args, status), idx+1
	}
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM media_items WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id,source_path,media_type,mime_type,file_size,file_hash,status,error_msg,metadata,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM media_items WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []*MediaItem
	for rows.Next() {
		var item MediaItem
		var meta []byte
		if err := rows.Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &meta, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if meta != nil {
			json.Unmarshal(meta, &item.Metadata)
		}
		items = append(items, &item)
	}
	return items, total, nil
}

func (s *Store) UpsertSummary(ctx context.Context, summary *Summary) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO summaries (id,media_id,summary,key_points,tags,ai_model,token_count) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (media_id) DO UPDATE SET summary=EXCLUDED.summary, key_points=EXCLUDED.key_points, tags=EXCLUDED.tags, ai_model=EXCLUDED.ai_model, token_count=EXCLUDED.token_count, created_at=NOW()`,
		summary.ID, summary.MediaID, summary.Summary, summary.KeyPoints, summary.Tags, summary.Model, summary.TokenCount)
	return err
}

func (s *Store) GetSummary(ctx context.Context, mediaID string) (*Summary, error) {
	var summary Summary
	err := s.pool.QueryRow(ctx, `SELECT id,media_id,summary,key_points,tags,ai_model,token_count,created_at FROM summaries WHERE media_id=$1`,
		mediaID).Scan(&summary.ID, &summary.MediaID, &summary.Summary, &summary.KeyPoints, &summary.Tags, &summary.AIProvider, &summary.TokenCount, &summary.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &summary, err
}

func (s *Store) UpsertClassification(ctx context.Context, cls *Classification) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO classifications (id,media_id,topic,tags,confidence,ai_model) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (media_id) DO UPDATE SET topic=EXCLUDED.topic, tags=EXCLUDED.tags, confidence=EXCLUDED.confidence, ai_model=EXCLUDED.ai_model`,
		cls.ID, cls.MediaID, cls.Topic, cls.Tags, cls.Confidence, cls.Model)
	return err
}

func (s *Store) GetClassification(ctx context.Context, mediaID string) (*Classification, error) {
	var cls Classification
	err := s.pool.QueryRow(ctx, `SELECT id,media_id,topic,tags,confidence,ai_model,created_at FROM classifications WHERE media_id=$1`,
		mediaID).Scan(&cls.ID, &cls.MediaID, &cls.Topic, &cls.Tags, &cls.Confidence, &cls.AIProvider, &cls.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &cls, err
}

func (s *Store) CreateTextChunk(ctx context.Context, chunk *TextChunk) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO text_chunks (id,media_id,chunk_index,chunk_text,token_count) VALUES ($1,$2,$3,$4,$5)`,
		chunk.ID, chunk.MediaID, chunk.ChunkIndex, chunk.ChunkText, chunk.TokenCount)
	return err
}

func (s *Store) CreateEmbedding(ctx context.Context, embedding *Embedding) error {
	vec := pgvector.NewVector(embedding.Embedding)
	_, err := s.pool.Exec(ctx, `INSERT INTO embeddings (id,chunk_id,embedding) VALUES ($1,$2,$3)`,
		embedding.ID, embedding.ChunkID, vec)
	return err
}

func (s *Store) SearchEmbeddings(ctx context.Context, queryEmbedding []float32, queryText string, topK int) ([]SearchResult, error) {
	vec := pgvector.NewVector(queryEmbedding)
	limit := topK * 2
	if limit < 10 {
		limit = 10
	}

	rows, err := s.pool.Query(ctx, `
		SELECT m.id,m.source_path,m.media_type,m.mime_type,m.file_size,m.file_hash,m.status,m.error_msg,m.metadata,CAST(m.created_at AS TEXT),CAST(m.updated_at AS TEXT),
			   tc.id,tc.chunk_text,tc.token_count,
			   1-(e.embedding<=>$1) AS score
		FROM embeddings e
		JOIN text_chunks tc ON tc.id=e.chunk_id
		JOIN media_items m ON m.id=tc.media_id
		WHERE m.status='ready'
		ORDER BY e.embedding<=>$1
		LIMIT $2`,
		vec, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var item MediaItem
		var meta []byte
		var tokenCount int
		if err := rows.Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &meta, &item.CreatedAt, &item.UpdatedAt,
			&r.ChunkID, &r.ChunkText, &tokenCount, &r.Score); err != nil {
			return nil, err
		}
		if meta != nil {
			json.Unmarshal(meta, &item.Metadata)
		}
		r.MediaItem = &item
		results = append(results, r)
	}

	if queryText != "" {
		ftRows, err := s.pool.Query(ctx, `
			SELECT m.id,m.source_path,m.media_type,m.mime_type,m.file_size,m.file_hash,m.status,m.error_msg,m.metadata,CAST(m.created_at AS TEXT),CAST(m.updated_at AS TEXT),
				   tc.id,tc.chunk_text,tc.token_count,
				   ts_rank(to_tsvector('english',tc.chunk_text),plainto_tsquery('english',$1)) AS score
			FROM text_chunks tc
			JOIN media_items m ON m.id=tc.media_id
			WHERE m.status='ready'
			  AND to_tsvector('english',tc.chunk_text) @@ plainto_tsquery('english',$1)
			ORDER BY score DESC
			LIMIT $2`,
			queryText, limit)
		if err == nil {
			defer ftRows.Close()
			for ftRows.Next() {
				var r SearchResult
				var item MediaItem
				var meta []byte
				var tokenCount int
				if err := ftRows.Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &meta, &item.CreatedAt, &item.UpdatedAt,
					&r.ChunkID, &r.ChunkText, &tokenCount, &r.Score); err != nil {
					continue
				}
				if meta != nil {
					json.Unmarshal(meta, &item.Metadata)
				}
				r.MediaItem = &item
				found := false
				for _, ex := range results {
					if ex.ChunkID == r.ChunkID {
						found = true
						break
					}
				}
				if !found {
					results = append(results, r)
				}
			}
		}
	}

	sortSearchResults(results)
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func (s *Store) TextSearch(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id,m.source_path,m.media_type,m.mime_type,m.file_size,m.file_hash,m.status,m.error_msg,m.metadata,CAST(m.created_at AS TEXT),CAST(m.updated_at AS TEXT),
			   tc.id,tc.chunk_text,tc.token_count,
			   ts_rank(to_tsvector('english',tc.chunk_text),plainto_tsquery('english',$1)) AS score
		FROM text_chunks tc
		JOIN media_items m ON m.id=tc.media_id
		WHERE m.status='ready'
		  AND to_tsvector('english',tc.chunk_text) @@ plainto_tsquery('english',$1)
		ORDER BY score DESC
		LIMIT $2`,
		query, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var item MediaItem
		var meta []byte
		var tokenCount int
		if err := rows.Scan(&item.ID, &item.SourcePath, &item.MediaType, &item.MimeType, &item.FileSize, &item.FileHash, &item.Status, &item.ErrorMsg, &meta, &item.CreatedAt, &item.UpdatedAt,
			&r.ChunkID, &r.ChunkText, &tokenCount, &r.Score); err != nil {
			return nil, err
		}
		if meta != nil {
			json.Unmarshal(meta, &item.Metadata)
		}
		r.MediaItem = &item
		results = append(results, r)
	}
	return results, nil
}

func (s *Store) Stats(ctx context.Context) (map[string]any, error) {
	var totalItems, totalChunks, totalReady int
	byType := make(map[string]int)
	byStatus := make(map[string]int)

	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM media_items").Scan(&totalItems)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM text_chunks").Scan(&totalChunks)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM media_items WHERE status='ready'").Scan(&totalReady)

	if rows, err := s.pool.Query(ctx, "SELECT media_type,COUNT(*) FROM media_items GROUP BY media_type"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			var c int
			rows.Scan(&t, &c)
			byType[t] = c
		}
	}
	if rows, err := s.pool.Query(ctx, "SELECT status,COUNT(*) FROM media_items GROUP BY status"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var st string
			var c int
			rows.Scan(&st, &c)
			byStatus[st] = c
		}
	}
	return map[string]any{
		"total_items":   totalItems,
		"total_chunks":  totalChunks,
		"total_ready":   totalReady,
		"by_media_type": byType,
		"by_status":     byStatus,
	}, nil
}

func sortSearchResults(results []SearchResult) {
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// === Pipeline ===

type Pipeline struct {
	store      *Store
	ai         AIProvider
	detector   *MediaDetector
	config     *Config
	extractors map[MediaType]ContentExtractor
}

func NewPipeline(store *Store, ai AIProvider, cfg *Config) *Pipeline {
	p := &Pipeline{
		store:      store,
		ai:         ai,
		detector:   NewMediaDetector(&cfg.Ingest),
		config:     cfg,
		extractors: make(map[MediaType]ContentExtractor),
	}
	p.extractors[MediaTypeText] = &TextExtractor{}
	p.extractors[MediaTypeDocument] = &DocxExtractor{}
	pdf := &PDFExtractor{}
	if pdf.Supported() {
		p.extractors[MediaTypeDocument] = pdf
	}
	p.extractors[MediaTypeImage] = &ImageExtractor{}
	return p
}

func (p *Pipeline) ProcessFile(ctx context.Context, path string, log *slog.Logger) (*MediaItem, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("use ProcessDirectory for directories")
	}

	maxSize := parseSize(p.config.Ingest.MaxFileSize)
	if info.Size() > int64(maxSize) {
		return nil, fmt.Errorf("file too large: %d > %d bytes", info.Size(), maxSize)
	}

	ext := filepath.Ext(path)
	if !p.config.Ingest.IsSupportedType(ext) {
		return nil, fmt.Errorf("unsupported type: %s", ext)
	}

	hash, err := FileHash(path)
	if err != nil {
		return nil, err
	}
	if existing, err := p.store.GetMediaItemByHash(ctx, hash); err == nil && existing != nil {
		log.Info("duplicate, skipping", "path", path, "id", existing.ID)
		return existing, nil
	}

	mediaType, mimeType, _ := p.detector.Detect(path)
	if mediaType == MediaTypeUnknown {
		mediaType = MediaTypeText
	}
	fileSize, _ := FileSize(path)

	item := &MediaItem{
		ID:         uuid.New().String(),
		SourcePath: path,
		MediaType:  mediaType,
		MimeType:   mimeType,
		FileSize:   fileSize,
		FileHash:   hash,
		Status:     StatusPending,
		CreatedAt:  time.Now().Format(time.RFC3339),
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}
	if err := p.store.CreateMediaItem(ctx, item); err != nil {
		return nil, err
	}

	log.Info("processing", "id", item.ID, "type", mediaType)
	p.store.UpdateMediaItemStatus(ctx, item.ID, StatusProcessing, "")

	text, err := p.extractContent(ctx, item)
	if err != nil {
		p.store.UpdateMediaItemStatus(ctx, item.ID, StatusFailed, err.Error())
		return item, err
	}

	chunks := ChunkText(text, p.config.Ingest.ChunkSize, p.config.Ingest.ChunkOverlap)
	chunkIDs := make([]string, len(chunks))
	for i, chunkText := range chunks {
		chunk := &TextChunk{
			ID:         uuid.New().String(),
			MediaID:    item.ID,
			ChunkIndex: i,
			ChunkText:  chunkText,
			TokenCount: countTokens(chunkText),
			CreatedAt:  time.Now().Format(time.RFC3339),
		}
		if err := p.store.CreateTextChunk(ctx, chunk); err != nil {
			log.Warn("create chunk failed", "err", err)
			continue
		}
		chunkIDs[i] = chunk.ID
	}

	if len(chunkIDs) > 0 && p.ai != nil {
		if err := p.generateEmbeddings(ctx, chunkIDs, chunks, log); err != nil {
			log.Warn("embeddings failed", "err", err)
		}
	}

	if text != "" && p.ai != nil {
		if summary, err := p.ai.Summarize(ctx, text); err != nil {
			log.Warn("summarize failed", "err", err)
		} else {
			summary.ID = uuid.New().String()
			summary.MediaID = item.ID
			summary.CreatedAt = time.Now().Format(time.RFC3339)
			p.store.UpsertSummary(ctx, summary)
		}
	}

	if text != "" && p.ai != nil {
		if cls, err := p.ai.Classify(ctx, text); err != nil {
			log.Warn("classify failed", "err", err)
		} else {
			cls.ID = uuid.New().String()
			cls.MediaID = item.ID
			cls.CreatedAt = time.Now().Format(time.RFC3339)
			p.store.UpsertClassification(ctx, cls)
		}
	}

	p.store.UpdateMediaItemStatus(ctx, item.ID, StatusReady, "")
	item.Status = StatusReady
	log.Info("done", "id", item.ID, "chunks", len(chunks))
	return item, nil
}

func (p *Pipeline) ProcessDirectory(ctx context.Context, dirPath string, recursive bool, log *slog.Logger) ([]*MediaItem, error) {
	var items []*MediaItem
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if !recursive && path != dirPath {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if !p.config.Ingest.IsSupportedType(ext) {
			return nil
		}
		item, err := p.ProcessFile(ctx, path, log)
		if err != nil {
			log.Warn("failed", "path", path, "err", err)
			return nil
		}
		if item != nil {
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

func (p *Pipeline) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	var results []SearchResult
	if p.ai != nil {
		embeddings, err := p.ai.Embed(ctx, []string{query})
		if err == nil && len(embeddings) > 0 {
			results, _ = p.store.SearchEmbeddings(ctx, embeddings[0], query, topK)
		}
	}

	if len(results) == 0 {
		results, _ = p.store.TextSearch(ctx, query, topK)
	}

	for i := range results {
		if results[i].MediaItem != nil {
			summary, _ := p.store.GetSummary(ctx, results[i].MediaItem.ID)
			results[i].Summary = summary
		}
	}
	return results, nil
}

func (p *Pipeline) extractContent(ctx context.Context, item *MediaItem) (string, error) {
	ext := strings.ToLower(filepath.Ext(item.SourcePath))
	switch {
	case ext == ".pdf", ext == ".doc", ext == ".docx":
		if e, ok := p.extractors[MediaTypeDocument]; ok {
			return e.Extract(ctx, item.SourcePath)
		}
	case ext == ".md", ext == ".markdown":
		if e, ok := p.extractors[MediaTypeText]; ok {
			return e.Extract(ctx, item.SourcePath)
		}
	case p.extractors[item.MediaType] != nil:
		return p.extractors[item.MediaType].Extract(ctx, item.SourcePath)
	default:
		if e, ok := p.extractors[MediaTypeText]; ok {
			return e.Extract(ctx, item.SourcePath)
		}
	}
	return "", nil
}

func (p *Pipeline) generateEmbeddings(ctx context.Context, chunkIDs []string, chunks []string, log *slog.Logger) error {
	if p.ai == nil {
		return nil
	}
	batchSize := 10
	for i := 0; i < len(chunks); i += batchSize {
		end := intMin(i+batchSize, len(chunks))
		batch, ids := chunks[i:end], chunkIDs[i:end]
		embeddings, err := p.ai.Embed(ctx, batch)
		if err != nil {
			log.Warn("embed batch failed", "err", err)
			continue
		}
		for j, emb := range embeddings {
			e := &Embedding{
				ID:        uuid.New().String(),
				ChunkID:   ids[j],
				Embedding: emb,
				CreatedAt: time.Now().Format(time.RFC3339),
			}
			if err := p.store.CreateEmbedding(ctx, e); err != nil {
				log.Warn("create embedding failed", "err", err)
			}
		}
	}
	return nil
}

// === Media Detection ===

type MediaDetector struct {
	config *IngestConfig
}

func NewMediaDetector(cfg *IngestConfig) *MediaDetector { return &MediaDetector{config: cfg} }

func (d *MediaDetector) Detect(path string) (MediaType, string, error) {
	ext := filepath.Ext(path)
	mimeType := d.detectMime(path, ext)
	switch {
	case d.isImage(ext, mimeType):
		return MediaTypeImage, mimeType, nil
	case d.isVideo(ext, mimeType):
		return MediaTypeVideo, mimeType, nil
	case d.isAudio(ext, mimeType):
		return MediaTypeAudio, mimeType, nil
	case d.isDocument(ext, mimeType):
		return MediaTypeDocument, mimeType, nil
	case d.isText(ext, mimeType):
		return MediaTypeText, mimeType, nil
	default:
		return MediaTypeUnknown, mimeType, fmt.Errorf("unsupported: %s", ext)
	}
}

func (d *MediaDetector) detectMime(path, ext string) string {
	if m := d.magicMime(path); m != "" {
		return m
	}
	if m := mime.TypeByExtension(ext); m != "" {
		return m
	}
	return "application/octet-stream"
}

func (d *MediaDetector) magicMime(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return ""
	}
	h := buf[:n]
	switch {
	case len(h) >= 8 && h[0] == 0x89 && h[1] == 'P' && h[2] == 'N' && h[3] == 'G':
		return "image/png"
	case len(h) >= 3 && h[0] == 0xFF && h[1] == 0xD8 && h[2] == 0xFF:
		return "image/jpeg"
	case len(h) >= 6 && h[0] == 'G' && h[1] == 'I' && h[2] == 'F':
		return "image/gif"
	case len(h) >= 12 && string(h[0:4]) == "RIFF" && string(h[8:12]) == "WEBP":
		return "image/webp"
	case len(h) >= 4 && h[0] == 0x25 && h[1] == 0x50 && h[2] == 0x44 && h[3] == 0x46:
		return "application/pdf"
	case len(h) >= 2 && h[0] == 0x42 && h[1] == 0x4D:
		return "image/bmp"
	}
	return ""
}

func (d *MediaDetector) isImage(ext, mime string) bool {
	if _, ok := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".svg": true, ".tiff": true}[strings.ToLower(ext)]; ok {
		return true
	}
	return strings.HasPrefix(mime, "image/")
}

func (d *MediaDetector) isVideo(ext, mime string) bool {
	if _, ok := map[string]bool{".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".webm": true}[strings.ToLower(ext)]; ok {
		return true
	}
	return strings.HasPrefix(mime, "video/")
}

func (d *MediaDetector) isAudio(ext, mime string) bool {
	if _, ok := map[string]bool{".mp3": true, ".wav": true, ".flac": true, ".aac": true, ".ogg": true, ".m4a": true}[strings.ToLower(ext)]; ok {
		return true
	}
	return strings.HasPrefix(mime, "audio/")
}

func (d *MediaDetector) isDocument(ext, mime string) bool {
	if _, ok := map[string]bool{".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true, ".odt": true}[strings.ToLower(ext)]; ok {
		return true
	}
	return strings.HasPrefix(mime, "application/pdf") || strings.HasPrefix(mime, "application/msword") || strings.HasPrefix(mime, "application/vnd.openxmlformats")
}

func (d *MediaDetector) isText(ext, mime string) bool {
	if _, ok := map[string]bool{".txt": true, ".md": true, ".markdown": true, ".html": true, ".htm": true, ".xml": true, ".json": true, ".csv": true, ".log": true, ".yaml": true, ".yml": true, ".toml": true, ".go": true, ".py": true, ".js": true, ".ts": true, ".java": true, ".c": true, ".cpp": true, ".h": true, ".rs": true, ".rb": true, ".php": true, ".sh": true, ".bat": true, ".ps1": true}[strings.ToLower(ext)]; ok {
		return true
	}
	return strings.HasPrefix(mime, "text/")
}

func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// === Content Extractors ===

type ContentExtractor interface {
	Extract(ctx context.Context, path string) (string, error)
	Supported() bool
}

type TextExtractor struct{}

func (e *TextExtractor) Supported() bool { return true }
func (e *TextExtractor) Extract(ctx context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

type MarkdownExtractor struct{}

func (e *MarkdownExtractor) Supported() bool { return true }
func (e *MarkdownExtractor) Extract(ctx context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

type PDFExtractor struct{}

func (e *PDFExtractor) Supported() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

func (e *PDFExtractor) Extract(ctx context.Context, path string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w (stderr: %s)", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

type DocxExtractor struct{}

func (e *DocxExtractor) Supported() bool { return true }

func (e *DocxExtractor) Extract(ctx context.Context, path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			return extractTextFromXML(data), nil
		}
	}
	return "", fmt.Errorf("no word/document.xml in docx")
}

type ImageExtractor struct{}

func (e *ImageExtractor) Supported() bool { return true }

func (e *ImageExtractor) Extract(ctx context.Context, path string) (string, error) {
	filename := filepath.Base(path)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return fmt.Sprintf("[图片: %s] 文件名: %s", filepath.Base(path), strings.TrimSpace(name)), nil
}

func extractTextFromXML(data []byte) string {
	var sb strings.Builder
	inTag := false
	scriptDepth := 0
	var tag []byte
	for i := 0; i < len(data); i++ {
		b := data[i]
		if !inTag && b == '<' {
			if i+7 < len(data) && string(data[i:i+7]) == "<script" {
				scriptDepth++
				inTag = true
				tag = tag[:0]
				continue
			}
			if i+6 < len(data) && string(data[i:i+6]) == "<style" {
				scriptDepth++
				inTag = true
				tag = tag[:0]
				continue
			}
		}
		if inTag {
			tag = append(tag, b)
			if b == '>' {
				tagStr := strings.ToLower(string(tag))
				if strings.HasPrefix(tagStr, "</script") || strings.HasPrefix(tagStr, "</style") {
					scriptDepth--
					if scriptDepth == 0 {
						inTag = false
						tag = tag[:0]
					}
				} else if scriptDepth == 0 {
					inTag = false
					sb.WriteByte(' ')
					tag = tag[:0]
				}
			}
		} else {
			if !unicode.IsControl(rune(b)) || b == '\n' || b == '\r' || b == '\t' {
				sb.WriteByte(b)
			}
		}
	}
	result := strings.TrimSpace(sb.String())
	result = strings.Join(strings.Fields(result), " ")
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	return result
}

func ChunkText(text string, chunkSize, overlap int) []string {
	if len(text) == 0 {
		return nil
	}
	charSize := chunkSize * 4
	var chunks []string
	start := 0
	for start < len(text) {
		end := start + charSize
		if end >= len(text) {
			chunk := strings.TrimSpace(text[start:])
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}
		breakPoint := findBreakPoint(text[start:end])
		if breakPoint > start+charSize/2 {
			end = breakPoint
		} else if idx := strings.LastIndexAny(text[start:end], "\n。；!?."); idx > charSize/4 {
			end = start + idx + 1
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = end - overlap
		if start >= len(text) {
			break
		}
	}
	return chunks
}

func findBreakPoint(s string) int {
	for _, bp := range []string{"\n\n", "\r\n\r\n", "\n", "。", "！", "？", "!", "?", ";", "；", ",", "，", ". "} {
		if idx := strings.LastIndex(s, bp); idx >= 0 {
			return idx + len(bp)
		}
	}
	return -1
}

func parseSize(s string) int {
	s = strings.ToUpper(strings.TrimSpace(s))
	factor := 1
	switch {
	case strings.HasSuffix(s, "GB"):
		factor = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		factor = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		factor = 1024
		s = strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	var val int
	fmt.Sscanf(s, "%d", &val)
	return val * factor
}
