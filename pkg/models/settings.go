package models

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type PluginSettings struct {
	Path     string                `json:"path"` // TODO - do I need this?
	ClientId string                `json:"clientId"`
	Secrets  *SecretPluginSettings `json:"-"`
}

type SecretPluginSettings struct {
	ClientSecret string `json:"clientSecret"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func LoadPluginSettings(source backend.DataSourceInstanceSettings) (*PluginSettings, error) {
	settings := PluginSettings{}
	err := json.Unmarshal(source.JSONData, &settings)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal PluginSettings json: %w", err)
	}

	settings.Secrets = loadSecretPluginSettings(source.DecryptedSecureJSONData)

	return &settings, nil
}

func loadSecretPluginSettings(source map[string]string) *SecretPluginSettings {
	if source == nil {
		return &SecretPluginSettings{}
	}
	return &SecretPluginSettings{
		ClientSecret: source["clientSecret"],
		AccessToken:  source["accessToken"],
		RefreshToken: source["refreshToken"],
	}
}

// AuthHeaderValue returns the value for the Authorization header for Oura API requests (Bearer token).
func (s *SecretPluginSettings) AuthHeaderValue() string {
	if s == nil || s.AccessToken == "" {
		return ""
	}
	return "Bearer " + s.AccessToken
}
