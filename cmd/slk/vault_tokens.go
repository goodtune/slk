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

// loadVaultTokens reads every workspace credential in secret. A field that is
// not a credential (issued_at, or something written by hand) is skipped; one
// that is but lacks what slk needs is skipped with a warning naming the field,
// never its value. The tokens are marked TokenSourceVault: memory only.
func loadVaultTokens(ctx context.Context, r vaultTokenReader, secret string) ([]slackclient.Token, error) {
	fields, found, err := r.ReadUserSecretFields(ctx, secret)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errVaultNoSecret
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []slackclient.Token
	for _, name := range names {
		var c vaultCredential
		if json.Unmarshal([]byte(fields[name]), &c) != nil || c.Domain == "" {
			continue
		}
		if c.AccessToken == "" || c.Cookie == "" || c.TeamID == "" {
			log.Printf("vault: %s field %q is missing a token, cookie or team ID; skipped", secret, name)
			continue
		}
		out = append(out, slackclient.Token{
			AccessToken: c.AccessToken,
			Cookie:      c.Cookie,
			Domain:      c.Domain,
			TeamID:      c.TeamID,
			TeamName:    c.TeamName,
			Source:      slackclient.TokenSourceVault,
		})
	}
	return out, nil
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
// is off; otherwise the tokens read, or a message for stderr saying what went
// wrong and what to do.
func vaultTokens(ctx context.Context, cfg config.Vault, connect func(context.Context, config.Vault) (vaultTokenReader, error)) ([]slackclient.Token, string) {
	if cfg.Secret == "" {
		return nil, ""
	}
	r, err := connect(ctx, cfg)
	if err == nil {
		var toks []slackclient.Token
		if toks, err = loadVaultTokens(ctx, r, cfg.Secret); err == nil {
			if len(toks) == 0 {
				return nil, "no Slack workspaces in " + cfg.Secret + "; run `dotvault enrol " + cfg.Secret + "`"
			}
			return toks, ""
		}
	}
	return nil, vaultErrorText(cfg.Secret, err)
}
