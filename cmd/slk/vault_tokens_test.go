package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	dotvault "github.com/goodtune/dotvault/client"

	"github.com/gammons/slk/internal/config"
	slackclient "github.com/gammons/slk/internal/slack"
)

// fakeVaultReader answers ReadUserSecretFields with fields (or err), and
// records the service asked for.
type fakeVaultReader struct {
	fields map[string]string
	found  bool
	err    error
	asked  string
}

func (f *fakeVaultReader) ReadUserSecretFields(_ context.Context, service string) (map[string]string, bool, error) {
	f.asked = service
	return f.fields, f.found, f.err
}

// dotvault's client must satisfy the interface slk depends on.
var _ vaultTokenReader = (*dotvault.Client)(nil)

func credJSON(domain, team string) string {
	return fmt.Sprintf(`{"domain":%q,"team_id":%q,"team_name":"Name %s","user_id":"U1","access_token":"xoxc-%s","cookie":"xoxd-c"}`,
		domain, team, domain, domain)
}

func TestLoadVaultTokens(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{
		"acme":       credJSON("acme", "T1"),
		"beta":       credJSON("beta", "T2"),
		"issued_at":  "2026-11-03T12:00:00Z",
		"expires_at": "2026-11-04T12:00:00Z",
		"note":       "hand-written",
		"broken":     `{"domain":"broken","access_token":"xoxc-x"}`, // no cookie or team
	}}
	toks, err := loadVaultTokens(context.Background(), r, "slack/slk")
	if err != nil {
		t.Fatal(err)
	}
	if r.asked != "slack/slk" {
		t.Errorf("asked for %q", r.asked)
	}
	if len(toks) != 2 {
		t.Fatalf("tokens = %+v, want acme and beta only", toks)
	}
	want := slackclient.Token{AccessToken: "xoxc-acme", Cookie: "xoxd-c", Domain: "acme", TeamID: "T1",
		TeamName: "Name acme", Source: slackclient.TokenSourceVault}
	if toks[0] != want {
		t.Errorf("acme = %+v, want %+v", toks[0], want)
	}
	if toks[1].TeamID != "T2" || toks[1].Source != slackclient.TokenSourceVault {
		t.Errorf("beta = %+v", toks[1])
	}
}

func TestLoadVaultTokensAbsentSecret(t *testing.T) {
	_, err := loadVaultTokens(context.Background(), &fakeVaultReader{}, "slack/slk")
	if !errors.Is(err, errVaultNoSecret) {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeVaultTokensReplacesSameTeam(t *testing.T) {
	files := []slackclient.Token{{TeamID: "T1", AccessToken: "file"}, {TeamID: "T3", AccessToken: "file3"}}
	vault := []slackclient.Token{{TeamID: "T1", AccessToken: "vault", Source: slackclient.TokenSourceVault}}
	got := mergeVaultTokens(files, vault)
	if len(got) != 2 || got[0].TeamID != "T3" || got[1].AccessToken != "vault" {
		t.Errorf("merged = %+v", got)
	}
}

func TestVaultTokensMessages(t *testing.T) {
	cfg := config.Vault{Secret: "slack/slk"}
	connect := func(err error, r vaultTokenReader) func(context.Context, config.Vault) (vaultTokenReader, error) {
		return func(context.Context, config.Vault) (vaultTokenReader, error) { return r, err }
	}
	cases := []struct {
		name string
		fn   func(context.Context, config.Vault) (vaultTokenReader, error)
		want string
	}{
		{"login", connect(fmt.Errorf("x: %w", dotvault.ErrLoginRequired), nil), "dotvault login"},
		{"unreachable", connect(fmt.Errorf("x: %w", dotvault.ErrUnreachable), nil), "unreachable"},
		{"denied", connect(nil, &fakeVaultReader{err: fmt.Errorf("x: %w", dotvault.ErrDenied)}), "cannot read slack/slk"},
		{"absent", connect(nil, &fakeVaultReader{}), "dotvault enrol slack/slk"},
		{"empty", connect(nil, &fakeVaultReader{found: true, fields: map[string]string{"issued_at": "x"}}), "no Slack workspaces"},
	}
	for _, c := range cases {
		toks, msg := vaultTokens(context.Background(), cfg, c.fn)
		if toks != nil || !strings.Contains(msg, c.want) {
			t.Errorf("%s: tokens=%v msg=%q, want one containing %q", c.name, toks, msg, c.want)
		}
	}

	called := false
	toks, msg := vaultTokens(context.Background(), config.Vault{}, func(context.Context, config.Vault) (vaultTokenReader, error) {
		called = true
		return nil, nil
	})
	if called || toks != nil || msg != "" {
		t.Error("[vault] off must not touch Vault")
	}
}

func TestRemintLeavesVaultTokensAlone(t *testing.T) {
	saved := 0
	tok := slackclient.Token{TeamID: "T1", Domain: "acme", AccessToken: "xoxc-vault", Cookie: "xoxd-v", Source: slackclient.TokenSourceVault}
	out := remintTokens(context.Background(), []slackclient.Token{tok},
		func() (string, error) { return "xoxd-desktop", nil },
		func() (map[string]string, error) { return map[string]string{"T1": "xoxc-desktop"}, nil },
		func(context.Context, string, string) (string, error) { return "xoxc-minted", nil },
		func(slackclient.Token) error { saved++; return nil },
	)
	if out[0] != tok || saved != 0 {
		t.Errorf("vault token re-minted or saved: %+v, saved %d", out[0], saved)
	}
}
