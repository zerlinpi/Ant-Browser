package cloudagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRequiresExplicitDistinctBindingsAndNoCredentials(t *testing.T) {
	const identity = `"deviceId":"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA","workspaceId":"BBBBBBBB-BBBB-4BBB-8BBB-BBBBBBBBBBBB",`
	cases := []struct {
		body  string
		valid bool
	}{
		{`{"baseUrl":"https://cloud.example.test",` + identity + `"bindings":{"AAAAAAAA-1111-4111-8111-111111111111":" local-one "}}`, true},
		{`{` + identity + `"bindings":{}}`, false},
		{`{` + identity + `"bindings":{"invalid":"local"}}`, false},
		{`{` + identity + `"bindings":{"11111111-1111-4111-8111-111111111111":"local","22222222-2222-4222-8222-222222222222":"local"}}`, false},
		{`{` + identity + `"credential":"secret","bindings":{"11111111-1111-4111-8111-111111111111":"local"}}`, false},
		{`{` + identity + `"bindings":{"11111111-1111-4111-8111-111111111111":"local"}} {}`, false},
		{`{"deviceId":"invalid","workspaceId":"BBBBBBBB-BBBB-4BBB-8BBB-BBBBBBBBBBBB","bindings":{"11111111-1111-4111-8111-111111111111":"local"}}`, false},
	}
	for _, test := range cases {
		path := filepath.Join(t.TempDir(), "agent.json")
		if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := LoadConfig(path)
		if (err == nil) != test.valid {
			t.Fatalf("config valid=%v err=%v", test.valid, err)
		}
		if test.valid {
			if config.DeviceID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || config.WorkspaceID != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" || config.Bindings["aaaaaaaa-1111-4111-8111-111111111111"] != "local-one" {
				t.Fatalf("identities were not canonicalized: %#v", config)
			}
		}
	}
	if _, err := LoadConfig("relative.json"); err == nil {
		t.Fatal("relative config path accepted")
	}
}
