package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/httpx"
)

// TestHeroesExample_EndToEnd wires up the same pieces main.go's run() does
// (minus signal handling) and exercises the service over real HTTP,
// including the exact policy file in ./policies — so a typo in the
// shipped .cedar file fails this test, not just a developer's first
// `go run .`.
func TestHeroesExample_EndToEnd(t *testing.T) {
	secret := []byte("test-secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})

	roles, err := authz.NewRoleRegistry(
		authz.Role{Name: "viewer"},
		authz.Role{Name: "editor", Parents: []string{"viewer"}},
		authz.Role{Name: "admin", Parents: []string{"editor"}},
	)
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}
	policyStore, err := authz.LoadPolicyDir("./policies")
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	authorizer := authz.New(policyStore, roles, nil)

	store := NewMemoryHeroStore()

	cfg := config.Base{
		ServiceName:            "heroes-example-test",
		HTTPHost:               "127.0.0.1",
		LogLevel:               "error",
		ShutdownTimeoutSeconds: 5,
		RequestTimeoutSeconds:  5,
		MaxBodyBytes:           1 << 20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := app.New(ctx, app.Options{Config: cfg, Verifier: verifier, Authorizer: authorizer})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	heroes := crud.NewResource(crud.Config[Hero, CreateHero, UpdateHero]{
		ResourceType:     "Hero",
		Store:            store,
		Authorizer:       authorizer,
		SortableFields:   map[string]bool{"name": true},
		FilterableFields: map[string]bool{"status": true},
	}, true)
	heroes.Post("/{id}/retire", httpx.Handle(retireHandler(store, authorizer, true), true))
	application.Mount("/v1/heroes", heroes)

	done := make(chan error, 1)
	go func() { done <- application.ListenAndServe(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	base := waitForAddr(t, application)

	adminToken, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice", Roles: []string{"admin"}}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}
	viewerToken, err := authn.SignHMAC(secret, authn.Principal{Subject: "bob", Roles: []string{"viewer"}}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}

	// Viewer cannot create.
	status, _ := doJSON(t, http.MethodPost, base+"/v1/heroes/", viewerToken, `{"name":"Clark","superPower":"flight"}`)
	if status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}

	// Admin can create.
	status, body := doJSON(t, http.MethodPost, base+"/v1/heroes/", adminToken, `{"name":"Clark","superPower":"flight"}`)
	if status != http.StatusCreated {
		t.Fatalf("admin create status = %d, body=%s", status, body)
	}
	var created Hero
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal created hero: %v", err)
	}

	// Viewer can read.
	status, _ = doJSON(t, http.MethodGet, base+"/v1/heroes/"+created.ID.String(), viewerToken, "")
	if status != http.StatusOK {
		t.Fatalf("viewer get status = %d, want 200", status)
	}

	// Retire once: OK. Retire again: forbidden by the ABAC policy.
	status, _ = doJSON(t, http.MethodPost, base+"/v1/heroes/"+created.ID.String()+"/retire", adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("first retire status = %d, want 200", status)
	}
	status, _ = doJSON(t, http.MethodPost, base+"/v1/heroes/"+created.ID.String()+"/retire", adminToken, "")
	if status != http.StatusForbidden {
		t.Fatalf("second retire status = %d, want 403 (already retired)", status)
	}
}

func doJSON(t *testing.T, method, url, token, body string) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.StatusCode, respBody
}

func waitForAddr(t *testing.T, a *app.App) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := a.Addr(); addr != "" {
			return "http://" + addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never reported a listen address")
	return ""
}
