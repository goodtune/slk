package config

import "testing"

func TestLoadVaultSection(t *testing.T) {
	cfg := loadConfigString(t, "[vault]\nsecret = \"slack/slk\"\nconfig = \"/etc/dv.yaml\"\n")
	if cfg.Vault.Secret != "slack/slk" || cfg.Vault.Config != "/etc/dv.yaml" {
		t.Errorf("vault = %+v", cfg.Vault)
	}
	if off := loadConfigString(t, ""); off.Vault.Secret != "" {
		t.Errorf("default must be off, got %+v", off.Vault)
	}
}
