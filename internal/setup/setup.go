package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"fox-gateway/internal/registry"
)

const (
	defaultWorkspaceRoot   = "~/.fox-gateway/workspace"
	defaultDBPath          = "~/.fox-gateway/data/fox-gateway.db"
	defaultClaudePath      = "claude"
	defaultReadOnlyWorkers = 2
)

func Run(in io.Reader, out io.Writer, configPath string) error {
	reg, err := registry.Open(configPath)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(in)
	current := reg.Config()

	printHeader(out, "Fox Gateway Setup", "This wizard only prepares local configuration. Feishu console changes still need to be done manually.")
	_, _ = fmt.Fprintf(out, "Configuration file: %s\n", reg.Path())
	_, _ = fmt.Fprintln(out, "You will need:")
	_, _ = fmt.Fprintln(out, "  - LARK_APP_ID")
	_, _ = fmt.Fprintln(out, "  - LARK_APP_SECRET")
	_, _ = fmt.Fprintln(out, "")

	if reg.HasConfig() {
		_, _ = fmt.Fprintln(out, "Existing local configuration detected. It will be replaced with the values entered below.")
		_, _ = fmt.Fprintln(out, "")
	}

	_, _ = fmt.Fprintln(out, "Feishu application credentials")
	_, _ = fmt.Fprintln(out, "-----------------------------")
	_, _ = fmt.Fprintln(out, "Paste the values from your Feishu app settings. The app secret stays local in fox-gateway.json.")
	appID, err := promptRequired(reader, out, "LARK_APP_ID", current.LarkAppID, current.LarkAppID)
	if err != nil {
		return err
	}
	appSecretDisplay := ""
	if current.LarkAppSecret != "" {
		appSecretDisplay = "configured"
	}
	appSecret, err := promptRequired(reader, out, "LARK_APP_SECRET", current.LarkAppSecret, appSecretDisplay)
	if err != nil {
		return err
	}

	cfg := registry.RuntimeConfig{
		DBPath:                defaultDBPath,
		LarkAppID:             appID,
		LarkAppSecret:         appSecret,
		LarkVerificationToken: registry.RandomHex(16),
		ClaudePath:            defaultClaudePath,
		WorkspaceRoot:         defaultWorkspaceRoot,
		MaxReadOnlyWorkers:    defaultReadOnlyWorkers,
	}
	if err := reg.SetConfig(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(registry.ExpandHome(defaultWorkspaceRoot), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(registry.ExpandHome(defaultDBPath)), 0o755); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out, "")
	printHeader(out, "Setup Complete", "Local configuration has been written successfully.")
	_, _ = fmt.Fprintf(out, "Configuration written to:\n  %s\n", reg.Path())
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Next step:")
	_, _ = fmt.Fprintln(out, "  ./fox-gateway")
	if message, ok := reg.BootstrapMessage(); ok {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "First approver pairing")
		_, _ = fmt.Fprintln(out, "----------------------")
		_, _ = fmt.Fprintln(out, "After the gateway starts, open the Feishu chat with the bot and send this exact message:")
		_, _ = fmt.Fprintf(out, "  %s\n", message)
	}
	return nil
}

func printHeader(out io.Writer, title, subtitle string) {
	_, _ = fmt.Fprintln(out, title)
	_, _ = fmt.Fprintln(out, strings.Repeat("=", len(title)))
	if subtitle != "" {
		_, _ = fmt.Fprintln(out, subtitle)
	}
	_, _ = fmt.Fprintln(out, "")
}

func printStep(out io.Writer, step, total int, title string) {
	line := fmt.Sprintf("Step %d/%d — %s", step, total, title)
	_, _ = fmt.Fprintln(out, line)
	_, _ = fmt.Fprintln(out, strings.Repeat("-", len(line)))
}

func promptRequired(reader *bufio.Reader, out io.Writer, label, current, displayCurrent string) (string, error) {
	for {
		if displayCurrent != "" {
			if _, err := fmt.Fprintf(out, "%s [%s]: ", label, displayCurrent); err != nil {
				return "", err
			}
		} else {
			if _, err := fmt.Fprintf(out, "%s: ", label); err != nil {
				return "", err
			}
		}
		value, err := readLine(reader)
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			value = current
		}
		if value != "" {
			return value, nil
		}
		_, _ = fmt.Fprintf(out, "%s cannot be empty.\n", label)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
