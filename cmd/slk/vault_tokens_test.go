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

// fakeMinter mints xoxc-minted-<domain> and identifies any token as team
// T-<domain>, recording what it was asked; failDomain fails both.
type fakeMinter struct {
	minted, identified []string
	failDomain         string
}

func (f *fakeMinter) minter() vaultMinter {
	return vaultMinter{
		mint: func(_ context.Context, domain, cookie string) (string, error) {
			f.minted = append(f.minted, domain+"/"+cookie)
			if domain == f.failDomain {
				return "", errors.New("api_token not found")
			}
			return "xoxc-minted-" + domain, nil
		},
		identify: func(token, _ string) (slackclient.Token, error) {
			f.identified = append(f.identified, token)
			domain := strings.TrimPrefix(token, "xoxc-minted-")
			return slackclient.Token{TeamID: "T-" + domain, TeamName: "Team " + domain, Domain: domain}, nil
		},
	}
}

func TestLoadVaultTokensUsesDotvaultsToken(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{
		"acme":       credJSON("acme", "T1"),
		"beta":       credJSON("beta", "T2"),
		"issued_at":  "2026-11-03T12:00:00Z",
		"expires_at": "2026-11-04T12:00:00Z",
		"note":       "hand-written",
	}}
	fm := &fakeMinter{}
	toks, warnings, err := loadVaultTokens(context.Background(), r, "slack/slk", fm.minter())
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
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
	if len(fm.minted) != 0 || len(fm.identified) != 0 {
		t.Errorf("a field with an access_token is dotvault's: nothing may be minted (%v) or identified (%v)", fm.minted, fm.identified)
	}
}

func TestLoadVaultTokensMintsWhenNoAccessToken(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{
		// Cookie only: slk mints and identifies.
		"acme": `{"domain":"acme","cookie":"xoxd-a"}`,
		// Cookie and team, no token: slk mints, the team is already known.
		"beta": `{"domain":"beta","cookie":"xoxd-b","team_id":"T2","team_name":"Beta"}`,
	}}
	fm := &fakeMinter{}
	toks, warnings, err := loadVaultTokens(context.Background(), r, "slack/slk", fm.minter())
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	if len(toks) != 2 {
		t.Fatalf("tokens = %+v", toks)
	}
	acme := slackclient.Token{AccessToken: "xoxc-minted-acme", Cookie: "xoxd-a", Domain: "acme", TeamID: "T-acme",
		TeamName: "Team acme", Source: slackclient.TokenSourceVault}
	if toks[0] != acme {
		t.Errorf("acme = %+v, want %+v", toks[0], acme)
	}
	if toks[1].AccessToken != "xoxc-minted-beta" || toks[1].TeamID != "T2" || toks[1].TeamName != "Beta" {
		t.Errorf("beta = %+v", toks[1])
	}
	if strings.Join(fm.minted, ",") != "acme/xoxd-a,beta/xoxd-b" {
		t.Errorf("minted = %v", fm.minted)
	}
	if strings.Join(fm.identified, ",") != "xoxc-minted-acme" {
		t.Errorf("identified = %v, want acme only (beta's team is in the field)", fm.identified)
	}
}

func TestLoadVaultTokensSkipsUnusableFields(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{
		"acme":    credJSON("acme", "T1"),
		"nocook":  `{"domain":"nocook","access_token":"xoxc-x","team_id":"T9"}`,
		"evil":    `{"domain":"evil.example/?","cookie":"xoxd-e"}`,
		"failing": `{"domain":"failing","cookie":"xoxd-f"}`,
	}}
	fm := &fakeMinter{failDomain: "failing"}
	toks, warnings, err := loadVaultTokens(context.Background(), r, "slack/slk", fm.minter())
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0].TeamID != "T1" {
		t.Errorf("tokens = %+v, want acme only", toks)
	}
	got := strings.Join(warnings, " | ")
	for _, want := range []string{"nocook: no cookie", "evil: domain", "failing: mint a token from the cookie"} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings %q lack %q", got, want)
		}
	}
	for _, m := range fm.minted {
		if strings.HasPrefix(m, "evil") {
			t.Error("the cookie was sent for a domain that is not a Slack subdomain")
		}
	}
	if strings.Contains(got, "xoxd-") || strings.Contains(got, "xoxc-") {
		t.Errorf("a warning carries a credential: %q", got)
	}
}

func TestLoadVaultTokensRefusesAMismatchedSession(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{"acme": `{"domain":"acme","cookie":"xoxd-a"}`}}
	m := vaultMinter{
		mint: func(context.Context, string, string) (string, error) { return "xoxc-x", nil },
		identify: func(string, string) (slackclient.Token, error) {
			return slackclient.Token{TeamID: "T9", Domain: "other"}, nil
		},
	}
	toks, warnings, _ := loadVaultTokens(context.Background(), r, "slack/slk", m)
	if len(toks) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "other.slack.com") {
		t.Errorf("tokens = %+v warnings = %v", toks, warnings)
	}
}

func TestLoadVaultTokensAbsentSecret(t *testing.T) {
	_, _, err := loadVaultTokens(context.Background(), &fakeVaultReader{}, "slack/slk", (&fakeMinter{}).minter())
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
		toks, msg := vaultTokens(context.Background(), cfg, c.fn, (&fakeMinter{}).minter())
		if toks != nil || !strings.Contains(msg, c.want) {
			t.Errorf("%s: tokens=%v msg=%q, want one containing %q", c.name, toks, msg, c.want)
		}
	}

	called := false
	toks, msg := vaultTokens(context.Background(), config.Vault{}, func(context.Context, config.Vault) (vaultTokenReader, error) {
		called = true
		return nil, nil
	}, (&fakeMinter{}).minter())
	if called || toks != nil || msg != "" {
		t.Error("[vault] off must not touch Vault")
	}
}

func TestVaultTokensPartialFailureStillLoads(t *testing.T) {
	r := &fakeVaultReader{found: true, fields: map[string]string{
		"acme":    credJSON("acme", "T1"),
		"failing": `{"domain":"failing","cookie":"xoxd-f"}`,
	}}
	connect := func(context.Context, config.Vault) (vaultTokenReader, error) { return r, nil }
	toks, msg := vaultTokens(context.Background(), config.Vault{Secret: "slack/slk"}, connect, (&fakeMinter{failDomain: "failing"}).minter())
	if len(toks) != 1 || !strings.Contains(msg, "failing") {
		t.Errorf("tokens = %+v msg = %q, want acme and a message naming failing", toks, msg)
	}
	r.fields = map[string]string{"failing": `{"domain":"failing","cookie":"xoxd-f"}`}
	toks, msg = vaultTokens(context.Background(), config.Vault{Secret: "slack/slk"}, connect, (&fakeMinter{failDomain: "failing"}).minter())
	if toks != nil || !strings.Contains(msg, "dotvault enrol slack/slk") {
		t.Errorf("all failing: tokens = %+v msg = %q", toks, msg)
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
