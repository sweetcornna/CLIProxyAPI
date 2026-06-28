// Package config provides the public SDK configuration API.
//
// It re-exports the server configuration types and helpers so external projects can
// embed CLIProxyAPI without importing internal packages.
package config

import internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"

type SDKConfig = internalconfig.SDKConfig

type Config = internalconfig.Config

type StreamingConfig = internalconfig.StreamingConfig
type TLSConfig = internalconfig.TLSConfig
type RemoteManagement = internalconfig.RemoteManagement
type OAuthModelAlias = internalconfig.OAuthModelAlias
type PayloadConfig = internalconfig.PayloadConfig
type PayloadRule = internalconfig.PayloadRule
type PayloadFilterRule = internalconfig.PayloadFilterRule
type PayloadModelRule = internalconfig.PayloadModelRule

type GeminiKey = internalconfig.GeminiKey
type CodexKey = internalconfig.CodexKey
type ClaudeKey = internalconfig.ClaudeKey
type VertexCompatKey = internalconfig.VertexCompatKey
type VertexCompatModel = internalconfig.VertexCompatModel
type OpenAICompatibility = internalconfig.OpenAICompatibility
type OpenAICompatibilityAPIKey = internalconfig.OpenAICompatibilityAPIKey
type OpenAICompatibilityModel = internalconfig.OpenAICompatibilityModel

type ModelMappingConfig = internalconfig.ModelMappingConfig
type ModelMappingRule = internalconfig.ModelMappingRule
type GroupConfig = internalconfig.GroupConfig
type RoutingConfig = internalconfig.RoutingConfig
type CompatConfig = internalconfig.CompatConfig

type TLS = internalconfig.TLSConfig

const (
	DefaultPanelGitHubRepository = internalconfig.DefaultPanelGitHubRepository
)

// Re-exported model-mapping / grouping helpers.
var (
	ResolveMappedModel       = internalconfig.ResolveMappedModel
	GroupForAPIKey           = internalconfig.GroupForAPIKey
	GroupByName              = internalconfig.GroupByName
	GroupHasModelScope       = internalconfig.GroupHasModelScope
	GroupAllowsModel         = internalconfig.GroupAllowsModel
	DefaultModelMappingRules = internalconfig.DefaultModelMappingRules
)

func LoadConfig(configFile string) (*Config, error) { return internalconfig.LoadConfig(configFile) }

func LoadConfigOptional(configFile string, optional bool) (*Config, error) {
	return internalconfig.LoadConfigOptional(configFile, optional)
}

func ParseConfigBytes(data []byte) (*Config, error) { return internalconfig.ParseConfigBytes(data) }

func SaveConfigPreserveComments(configFile string, cfg *Config) error {
	return internalconfig.SaveConfigPreserveComments(configFile, cfg)
}

func SaveConfigPreserveCommentsUpdateNestedScalar(configFile string, path []string, value string) error {
	return internalconfig.SaveConfigPreserveCommentsUpdateNestedScalar(configFile, path, value)
}

func NormalizeCommentIndentation(data []byte) []byte {
	return internalconfig.NormalizeCommentIndentation(data)
}
