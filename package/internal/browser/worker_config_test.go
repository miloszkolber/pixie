package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBrowserLaunchConfig(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{name: "hardened", args: "--no-sandbox,--deny-permission-prompts,--disable-notifications"},
		{name: "missing permission denial", args: "--no-sandbox", wantErr: true},
		{name: "ignore certificate errors", args: "--deny-permission-prompts,--ignore-certificate-errors", wantErr: true},
		{name: "ignore https errors", args: "--deny-permission-prompts,--ignore-https-errors", wantErr: true},
		{name: "file access", args: "--deny-permission-prompts,--allow-file-access", wantErr: true},
		{name: "file access from files", args: "--deny-permission-prompts,--allow-file-access-from-files", wantErr: true},
		{name: "web security disabled", args: "--deny-permission-prompts,--disable-web-security", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"args":"`+testCase.args+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			err := validateBrowserLaunchConfig(path)
			if testCase.wantErr && err == nil {
				t.Fatal("expected an unsafe browser config to be rejected")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("hardened browser config was rejected: %v", err)
			}
		})
	}
}

func TestValidateBrowserLaunchConfigRequiresAPath(t *testing.T) {
	if err := validateBrowserLaunchConfig(""); err == nil {
		t.Fatal("empty browser config path was accepted")
	}
}
