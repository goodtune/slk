package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	dotvault "github.com/goodtune/dotvault/client"

	"github.com/gammons/slk/internal/config"
	slackclient "github.com/gammons/slk/internal/slack"
)

// vaultCredential is one workspace field of the secret dotvault's slack
// engine writes. The field names are the contract with dotvault
// (dotvault docs/services/slack.md); slk imports dotvault's client and
// nothing else of it, and dotvault imports nothing of slk's.
type vaultCredential struct {
	Domain      string `json:"domain"`
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
	UserID      string `json:"user_id"`
	AccessToken string `json:"access_token"`
	Cookie      string `json:"cookie"`
}

// errVaultNoSecret is a configured secret with nothing at its path.
var errVaultNoSecret = errors.New("nothing is enrolled there")

// vaultTokenReader is the part of dotvault's client loadVaultTokens needs,
// so tests fake it without a Vault.
type vaultTokenReader interface {
	ReadUserSecretFields(ctx context.Context, service string) (map[string]string, bool, error)
}

// connectVault loads dotvault's configuration and authenticates the way
// dotvault's own non-interactive callers do: environment, token file, the
// daemon's socket, a client certificate. It never prompts or opens a
// browser.
func connectVault(ctx context.Context, cfg config.Vault) (vaultTokenReader, error) {
	path := cfg.Config
	if path == "" {
		path = dotvault.DefaultConfigPath()
	}
	dcfg, err := dotvault.LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("load dotvault config %s: %w", path, err)
	}
	cli, err := dotvault.New(dcfg)
	if err != nil {
		return nil, err
	}
	if err := cli.AuthenticateCached(ctx); err != nil {
		return nil, err
	}
	return cli, nil
}

// vaultMinter is how slk signs in to a workspace whose Vault field carries no
// access_token: mint an xoxc from the d cookie, then auth.test it for the
// team. MintToken and authBrowserSession in production; faked in tests.
type vaultMinter struct {
	mint     func(ctx context.Context, domain, cookie string) (string, error)
	identify func(token, cookie string) (slackclient.Token, error)
}

var defaultVaultMinter = vaultMinter{mint: slackclient.MintToken, identify: authBrowserSession}

// loadVaultTokens reads every workspace credential in secret. Who keeps the
// access token is decided per field by its presence: with an access_token,
// dotvault manages it (its slack engine re-mints it) and slk uses it as is;
// without one, slk mints it from the cookie itself, here, in memory. Either
// way a field missing its team ID is identified with auth.test.
//
// A field that is not a credential (issued_at, or something written by hand)
// is skipped silently. One that is but cannot be used (no cookie, a domain
// that is not a Slack subdomain, a mint or auth.test that fails) is skipped
// with a warning naming the field, never its value; the other workspaces
// still load. The tokens are marked TokenSourceVault: memory only.
func loadVaultTokens(ctx context.Context, r vaultTokenReader, secret string, m vaultMinter) ([]slackclient.Token, []string, error) {
	fields, found, err := r.ReadUserSecretFields(ctx, secret)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, errVaultNoSecret
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []slackclient.Token
	var warnings []string
	for _, name := range names {
		var c vaultCredential
		if json.Unmarshal([]byte(fields[name]), &c) != nil || c.Domain == "" {
			continue
		}
		tok, err := vaultCredentialToken(ctx, c, m)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		out = append(out, tok)
	}
	return out, warnings, nil
}

// vaultCredentialToken turns one workspace field into a token, minting and
// identifying as the field requires.
func vaultCredentialToken(ctx context.Context, c vaultCredential, m vaultMinter) (slackclient.Token, error) {
	if c.Cookie == "" {
		return slackclient.Token{}, errors.New("no cookie")
	}
	// The domain becomes the host the cookie is sent to, and the field may
	// have been written by hand.
	if err := validSlackSubdomain(c.Domain); err != nil {
		return slackclient.Token{}, fmt.Errorf("domain %q: %w", c.Domain, err)
	}
	tok := slackclient.Token{
		AccessToken: c.AccessToken,
		Cookie:      c.Cookie,
		Domain:      c.Domain,
		TeamID:      c.TeamID,
		TeamName:    c.TeamName,
		Source:      slackclient.TokenSourceVault,
	}
	if tok.AccessToken == "" {
		minted, err := m.mint(ctx, c.Domain, c.Cookie)
		if err != nil {
			return slackclient.Token{}, fmt.Errorf("mint a token from the cookie: %w", err)
		}
		tok.AccessToken = minted
	}
	if tok.TeamID == "" {
		who, err := m.identify(tok.AccessToken, tok.Cookie)
		if err != nil {
			return slackclient.Token{}, fmt.Errorf("auth.test: %w", err)
		}
		tok.TeamID, tok.TeamName = who.TeamID, who.TeamName
		if who.Domain != "" && who.Domain != c.Domain {
			return slackclient.Token{}, fmt.Errorf("the session is for %s.slack.com, not %s.slack.com", who.Domain, c.Domain)
		}
	}
	return tok, nil
}

// validSlackSubdomain is Slack's workspace URL rule: a-z, 0-9 and '-', 1-63
// bytes, no leading or trailing '-'. Being one DNS label is what makes it safe
// to put in front of .slack.com.
func validSlackSubdomain(s string) error {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return errors.New("not a Slack subdomain")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return errors.New("not a Slack subdomain")
		}
	}
	return nil
}

// mergeVaultTokens puts the Vault tokens in place of any token file for the
// same team: Vault is where the user moved that workspace. The files are left
// alone.
func mergeVaultTokens(files, vault []slackclient.Token) []slackclient.Token {
	fromVault := make(map[string]bool, len(vault))
	for _, t := range vault {
		fromVault[t.TeamID] = true
	}
	out := make([]slackclient.Token, 0, len(files)+len(vault))
	for _, t := range files {
		if !fromVault[t.TeamID] {
			out = append(out, t)
		}
	}
	return append(out, vault...)
}

// vaultErrorText words a failure to read the Vault secret as what to do
// about it.
func vaultErrorText(secret string, err error) string {
	switch {
	case errors.Is(err, dotvault.ErrLoginRequired):
		return "no usable Vault token; run `dotvault login` (or start the dotvault daemon)"
	case errors.Is(err, dotvault.ErrUnreachable):
		return "Vault is unreachable"
	case errors.Is(err, dotvault.ErrDenied):
		return "your Vault token cannot read " + secret
	case errors.Is(err, errVaultNoSecret):
		return "nothing is enrolled at " + secret + "; run `dotvault enrol " + secret + "`"
	}
	return strings.TrimSpace(err.Error())
}

// vaultTokens is startup's whole Vault step: nil and no message when [vault]
// is off; otherwise the tokens read, and a message for stderr or a toast
// saying what went wrong and what to do (also when only some workspaces
// failed).
func vaultTokens(ctx context.Context, cfg config.Vault, connect func(context.Context, config.Vault) (vaultTokenReader, error), m vaultMinter) ([]slackclient.Token, string) {
	if cfg.Secret == "" {
		return nil, ""
	}
	r, err := connect(ctx, cfg)
	if err != nil {
		return nil, vaultErrorText(cfg.Secret, err)
	}
	toks, warnings, err := loadVaultTokens(ctx, r, cfg.Secret, m)
	if err != nil {
		return nil, vaultErrorText(cfg.Secret, err)
	}
	if len(warnings) > 0 {
		for _, w := range warnings {
			log.Printf("vault %s: %s", cfg.Secret, w)
		}
		msg := "could not use " + strings.Join(warnings, "; ")
		if len(toks) == 0 {
			msg += "; run `dotvault enrol " + cfg.Secret + "` to sign in again"
		}
		return toks, msg
	}
	if len(toks) == 0 {
		return nil, "no Slack workspaces in " + cfg.Secret + "; run `dotvault enrol " + cfg.Secret + "`"
	}
	return toks, ""
}
