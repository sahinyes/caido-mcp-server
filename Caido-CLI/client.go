package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	caido "github.com/caido-community/sdk-go"
	"github.com/spf13/cobra"
)

type storedToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func loadToken() (*storedToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	data, err := os.ReadFile(
		filepath.Join(home, ".caido-mcp", "token.json"),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"no token found -- run 'caido-mcp-server login' first: %w",
			err,
		)
	}
	var t storedToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("bad token file: %w", err)
	}
	return &t, nil
}

func getCaidoURL(cmd *cobra.Command) (string, error) {
	u, _ := cmd.Flags().GetString("url")
	if u == "" {
		u = os.Getenv("CAIDO_URL")
	}
	if u == "" {
		return "", fmt.Errorf(
			"Caido URL required: set --url or CAIDO_URL env",
		)
	}
	return u, nil
}

func newClient(cmd *cobra.Command) (*caido.Client, error) {
	url, err := getCaidoURL(cmd)
	if err != nil {
		return nil, err
	}
	tok, err := loadToken()
	if err != nil {
		return nil, err
	}

	client, err := caido.NewClient(caido.Options{URL: url})
	if err != nil {
		return nil, fmt.Errorf("client init: %w", err)
	}
	client.SetAccessToken(tok.AccessToken)

	client.SetTokenRefresher(func(ctx context.Context) (string, error) {
		t, err := loadToken()
		if err != nil || t == nil {
			return "", nil
		}
		if time.Now().Add(5 * time.Minute).Before(t.ExpiresAt) {
			return t.AccessToken, nil
		}
		if t.RefreshToken == "" {
			return "", fmt.Errorf("token expired, no refresh token")
		}
		resp, err := client.Auth.RefreshAuthenticationToken(
			ctx, t.RefreshToken,
		)
		if err != nil {
			return "", err
		}
		payload := resp.RefreshAuthenticationToken
		if payload.Error != nil || payload.Token == nil {
			return "", fmt.Errorf("token refresh failed")
		}
		refreshTok := ""
		if payload.Token.RefreshToken != nil {
			refreshTok = *payload.Token.RefreshToken
		}
		expiresAt, parseErr := time.Parse(
			time.RFC3339, payload.Token.ExpiresAt,
		)
		if parseErr != nil {
			expiresAt = time.Now().Add(7 * 24 * time.Hour)
		}
		stored := &storedToken{
			AccessToken:  payload.Token.AccessToken,
			RefreshToken: refreshTok,
			ExpiresAt:    expiresAt,
		}
		data, _ := json.MarshalIndent(stored, "", "  ")
		home, _ := os.UserHomeDir()
		_ = os.WriteFile(
			filepath.Join(home, ".caido-mcp", "token.json"),
			data, 0600,
		)
		return stored.AccessToken, nil
	})

	return client, nil
}
