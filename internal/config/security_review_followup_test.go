package config

import "testing"

func TestReviewClonePreservesIDsAndIndependentMutableFields(t *testing.T) {
	enabled, upload := true, int64(42)
	original := &Config{
		App:     AppConfig{AutoOpenBrowser: &enabled, UploadMaxSize: &upload, CredentialKey: "key", SSHLogMaxMB: 20},
		Auth:    AuthConfig{Enabled: enabled},
		Systems: []SystemConfig{{ID: "stable-system-id", Name: "system"}},
		Pet:     PetConfig{NotifyUp: &enabled, ExpRules: map[string]int64{"x": 1}, StageLevels: map[string][2]int{"x": {1, 2}}, OpDailyMax: map[string]int64{"x": 3}},
	}
	cloned := original.Clone()
	if cloned.Systems[0].ID != "stable-system-id" || cloned.App.CredentialKey != "key" || cloned.App.SSHLogMaxMB != 20 {
		t.Fatal("clone lost values")
	}
	*cloned.App.AutoOpenBrowser = false
	cloned.Auth.Enabled = false
	*cloned.Pet.NotifyUp = false
	*cloned.App.UploadMaxSize = 0
	cloned.Pet.ExpRules["x"] = 9
	cloned.Pet.StageLevels["x"] = [2]int{9, 9}
	cloned.Pet.OpDailyMax["x"] = 9
	if !enabled || upload != 42 || original.Pet.ExpRules["x"] != 1 || original.Pet.StageLevels["x"] != [2]int{1, 2} || original.Pet.OpDailyMax["x"] != 3 {
		t.Fatal("clone mutations leaked into original")
	}
}
