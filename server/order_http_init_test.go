package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOrderLocalInitSupportsVerifiedCleanup(t *testing.T) {
	originalDataDir := serverConfig.DataDir
	t.Cleanup(func() { serverConfig.DataDir = originalDataDir })
	for _, tc := range []struct {
		name     string
		local    bool
		host     string
		manual   bool
		verified bool
		cleanup  string
		count    int
		valid    bool
	}{
		{"verified cleanup in local mode", true, "127.0.0.1", false, true, "/scripts/cleanup.lua", 1, true},
		{"manual mode remains supported", true, "127.0.0.1", true, false, "", 1, true},
		{"local mode rejects public host", true, "192.168.1.2", false, true, "/scripts/cleanup.lua", 1, false},
		{"local mode still one device", true, "127.0.0.1", false, true, "/scripts/cleanup.lua", 2, false},
		{"local mode rejects unverified automatic cleanup", true, "127.0.0.1", false, false, "/scripts/cleanup.lua", 1, false},
		{"automatic cleanup requires script path", true, "127.0.0.1", false, true, "", 1, false},
		{"manual profile forbidden outside local mode", false, "127.0.0.1", true, false, "", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverConfig.DataDir = t.TempDir()
			local := "0"
			if tc.local {
				local = "1"
			}
			t.Setenv("XXTCC_LOCAL_ORDER_TEST", local)
			t.Setenv("XXTCC_PUBLIC_URL", "http://"+tc.host+":46980")
			profiles := []orderProfile{}
			for i := 0; i < tc.count; i++ {
				profiles = append(profiles, orderProfile{DeviceID: string(rune('a' + i)), BundleID: "test.game", Currency: "CNY", PolicyPath: "/policy.plist", ScriptsDir: "/scripts", CleanupScript: tc.cleanup, ManualTest: tc.manual, Verified: tc.verified})
			}
			data, err := json.Marshal(profiles)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(serverConfig.DataDir, "order-profiles.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			closeService, err := initOrderHTTP(gin.New())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if closeService != nil {
				closeService()
			}
		})
	}
}
