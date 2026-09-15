package assistantconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAcceptsOnlyAssistantConfigV2(t *testing.T) {
	path := writeConfig(t, `{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false}`)
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "127.0.0.1" || config.Port != 3284 || config.AgentDir != "/var/lib/pi" || config.AllowSelfRestart {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadRejectsPublicPackageSelectionAndIncompleteV2Config(t *testing.T) {
	for _, contents := range []string{
		`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false,"piPackage":"/outside/pi"}`,
		`{"schemaVersion":1,"host":"127.0.0.1","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"/var/lib/pi"}`,
		`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"relative","allowSelfRestart":false}`,
	} {
		_, err := Load(writeConfig(t, contents))
		if err == nil {
			t.Fatalf("Load accepted %s", contents)
		}
		if strings.Contains(contents, "piPackage") && !strings.Contains(err.Error(), "must not select piPackage") {
			t.Fatalf("package-selection error = %q", err)
		}
	}
}
