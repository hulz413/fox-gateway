package setup

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"fox-gateway/internal/registry"
)

func TestRunWritesConfigAndBootstrapMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fox-gateway.json")
	input := bytes.NewBufferString("cli_test\nsecret\n")
	output := &bytes.Buffer{}

	if err := Run(input, output, path); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	reg, err := registry.Open(path)
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	cfg := reg.Config()
	if cfg.LarkAppID != "cli_test" || cfg.LarkAppSecret != "secret" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !strings.Contains(output.String(), "Fox Gateway Setup") {
		t.Fatalf("expected title-cased setup header: %s", output.String())
	}
	if !strings.Contains(output.String(), "Feishu application credentials") {
		t.Fatalf("expected credentials section header: %s", output.String())
	}
	if strings.Contains(output.String(), "Step 1/1") || strings.Contains(output.String(), "Step 2/3") || strings.Contains(output.String(), "Step 3/3") {
		t.Fatalf("unexpected step markers in simplified setup: %s", output.String())
	}
	if !strings.Contains(output.String(), "Setup Complete") {
		t.Fatalf("unexpected setup output: %s", output.String())
	}
	if !strings.Contains(output.String(), "Configuration written to:") {
		t.Fatalf("expected config path in completion output: %s", output.String())
	}
	if strings.Contains(output.String(), "Verification token:") {
		t.Fatalf("verification token should not be printed: %s", output.String())
	}
	if !strings.Contains(output.String(), "After the gateway starts, open the Feishu chat with the bot and send this exact message:") {
		t.Fatalf("expected pairing instructions in completion output: %s", output.String())
	}
	if strings.Contains(output.String(), "Overwrite the existing local configuration?") {
		t.Fatalf("setup should not ask for overwrite confirmation: %s", output.String())
	}
	if strings.Contains(output.String(), "Write local configuration now?") || strings.Contains(output.String(), "Continue [Y/n]") {
		t.Fatalf("setup should not ask for final confirmation: %s", output.String())
	}
	if message, ok := reg.BootstrapMessage(); !ok || !strings.HasPrefix(message, registry.RegisterCommandPrefix+" ") {
		t.Fatalf("unexpected bootstrap message: %q, %v", message, ok)
	}
}

func TestRunReplacesExistingConfigWithoutPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fox-gateway.json")
	reg, err := registry.Open(path)
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	if err := reg.SetConfig(registry.RuntimeConfig{
		DBPath:                defaultDBPath,
		LarkAppID:             "old_id",
		LarkAppSecret:         "old_secret",
		LarkVerificationToken: "old_token",
		ClaudePath:            defaultClaudePath,
		WorkspaceRoot:         defaultWorkspaceRoot,
		MaxReadOnlyWorkers:    defaultReadOnlyWorkers,
	}); err != nil {
		t.Fatalf("SetConfig error = %v", err)
	}

	output := &bytes.Buffer{}
	input := bytes.NewBufferString("new_id\nnew_secret\n")
	if err := Run(input, output, path); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	updated, err := registry.Open(path)
	if err != nil {
		t.Fatalf("registry.Open error = %v", err)
	}
	cfg := updated.Config()
	if cfg.LarkAppID != "new_id" || cfg.LarkAppSecret != "new_secret" {
		t.Fatalf("unexpected updated config: %+v", cfg)
	}
	if !strings.Contains(output.String(), "Existing local configuration detected. It will be replaced") {
		t.Fatalf("expected overwrite notice, got: %s", output.String())
	}
}
