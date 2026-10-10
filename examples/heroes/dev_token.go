package main

import (
	"context"
	"time"

	"github.com/herlenashavi/go-api/pkg/authn"
)

type devTokenRequest struct {
	Subject  string   `json:"subject" validate:"required"`
	TenantID string   `json:"tenantId"`
	Roles    []string `json:"roles"`
}

type devTokenResponse struct {
	Token string `json:"token"`
}

// devTokenHandler mints an HMAC-signed token for whatever subject/roles the
// caller asks for. That's only acceptable because main.go wires this route
// up ONLY when cfg.Debug is true, and it signs with the same dev secret
// authn.NewHMACVerifier checks — see that function's doc comment. A real
// service never has an endpoint like this; a real IdP issues tokens.
func devTokenHandler(secret []byte, debug bool) func(ctx context.Context, in *devTokenRequest) (*devTokenResponse, error) {
	return func(ctx context.Context, in *devTokenRequest) (*devTokenResponse, error) {
		token, err := authn.SignHMAC(secret, authn.Principal{
			Subject:  in.Subject,
			TenantID: in.TenantID,
			Roles:    in.Roles,
		}, time.Hour)
		if err != nil {
			return nil, err
		}
		return &devTokenResponse{Token: token}, nil
	}
}
