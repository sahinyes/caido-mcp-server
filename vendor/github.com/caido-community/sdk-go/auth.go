package caido

import (
	"context"

	gen "github.com/caido-community/sdk-go/graphql"
)

// AuthSDK provides operations for authentication flows.
type AuthSDK struct {
	client *Client
}

// StartAuthenticationFlow initiates an OAuth device-code flow.
func (s *AuthSDK) StartAuthenticationFlow(
	ctx context.Context,
) (*gen.StartAuthenticationFlowResponse, error) {
	return gen.StartAuthenticationFlow(ctx, s.client.GraphQL)
}

// RefreshAuthenticationToken exchanges a refresh token for new tokens.
func (s *AuthSDK) RefreshAuthenticationToken(
	ctx context.Context, refreshToken string,
) (*gen.RefreshAuthenticationTokenResponse, error) {
	return gen.RefreshAuthenticationToken(
		ctx, s.client.GraphQL, refreshToken,
	)
}

// GetAuthenticationState returns whether the instance allows guests.
func (s *AuthSDK) GetAuthenticationState(
	ctx context.Context,
) (*gen.GetAuthenticationStateResponse, error) {
	return gen.GetAuthenticationState(ctx, s.client.GraphQL)
}
